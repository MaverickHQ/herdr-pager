package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devsim"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdrtest"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/risk"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/status"
)

// serveHome gives the test its own HOME, in a short temporary path, holding
// the bridge's directory with a config.toml that listens on 127.0.0.1:0 and
// names a herdr socket of its own; it returns the bridge's directory and
// that socket's path.
func serveHome(t *testing.T) (dir, socket string) {
	t.Helper()
	home, err := os.MkdirTemp("", "hp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	dir = filepath.Join(home, ".config", "herdr-pager")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	socket = filepath.Join(home, "herdr.sock")
	cfg := fmt.Sprintf("listen = \"127.0.0.1:0\"\nherdr_socket = %q\n", socket)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, socket
}

// pairServe writes into dir the host key that init makes, and a devices.toml
// that pairs a device by each of names; it returns the host key's public half
// and the devices' keys, in order.
func pairServe(t *testing.T, dir string, names ...string) (host ssh.PublicKey, devices []ssh.Signer) {
	t.Helper()
	keys := make([]ssh.Signer, 1+len(names))
	var toml strings.Builder
	for i := range keys {
		_, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		if keys[i], err = ssh.NewSignerFromKey(priv); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			block, err := ssh.MarshalPrivateKey(priv, "")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "host_ed25519"), pem.EncodeToMemory(block), 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(keys[i].PublicKey())))
		fmt.Fprintf(&toml, "[[device]]\nname  = %q\nkey   = %q\nadded = 2026-10-22T10:00:00Z\n\n", names[i-1], line)
	}
	if err := os.WriteFile(filepath.Join(dir, "devices.toml"), []byte(toml.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return keys[0].PublicKey(), keys[1:]
}

// A serveProc is serve running as a process.
type serveProc struct {
	cmd  *exec.Cmd
	addr string        // where it listens
	done chan struct{} // closed once it has exited
	code int           // its exit code, once done
	log  serveLog      // its stderr
}

// A serveLog is a process's stderr as it comes.
type serveLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *serveLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *serveLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// bridgeCommand is `pager-bridge <name>` (serve or stdio), to run in the
// test's HOME.
func bridgeCommand(t *testing.T, name string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], name)
	cmd.Env = append(os.Environ(), runMainEnv+"=1")
	// Under -race, a process sleeps 1 s before it exits; the command's exit is
	// timed.
	cmd.Env = append(cmd.Env, "GORACE="+strings.TrimSpace(os.Getenv("GORACE")+" atexit_sleep_ms=0"))
	return cmd
}

// startServe starts `pager-bridge serve` as a process in the test's HOME
// (bridgeCommand) and returns once it listens; it's killed when the test
// ends, if it's still running.
func startServe(t *testing.T) *serveProc {
	t.Helper()
	cmd := bridgeCommand(t, "serve")
	p := &serveProc{cmd: cmd, done: make(chan struct{})}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		io.Copy(&p.log, stderr)
		cmd.Wait()
		p.code = cmd.ProcessState.ExitCode()
		close(p.done)
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		<-p.done
	})
	listening := regexp.MustCompile(`msg="serve: listening" addr=(\S+)`)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if m := listening.FindStringSubmatch(p.log.String()); m != nil {
			p.addr = m[1]
			return p
		}
		select {
		case <-p.done:
			t.Fatalf("serve exited %d without listening; stderr:\n%s", p.code, p.log.String())
		default:
		}
	}
	t.Fatalf("serve didn't listen within 10 s; stderr:\n%s", p.log.String())
	return nil
}

// pagerBridgeWithin runs the CLI as a process with args and stdin closed, as
// pagerBridge does, but kills it after timeout (exit -1).
func pagerBridgeWithin(t *testing.T, timeout time.Duration, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Env = append(os.Environ(), runMainEnv+"=1")
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		code = exit.ExitCode()
	case err != nil:
		t.Fatal(err)
	}
	return out.String(), errOut.String(), code
}

// badRules is a user's risk.toml whose second rule (lines 8 to 12) fails one
// of its own cases: its pattern doesn't match its second tests_match.
const badRules = `# Danger rules of my own.
[[rule]]
label          = "DEPLOY"
pattern        = '''\bfly\s+deploy\b'''
tests_match    = ["fly deploy --app api"]
tests_no_match = ["fly status"]

[[rule]]
label          = "DROP TABLE"
pattern        = '''(?i)\bdrop\s+table\b'''
tests_match    = ["psql -c 'DROP TABLE users'", "psql -c 'TRUNCATE users'"]
tests_no_match = ["psql -c 'SELECT 1'"]
`

// TestServeRefusesBadRiskOverride checks parent 18.2 and parent design
// §Danger rules: serve refuses to start on a user rule whose own cases fail.
// It exits non-zero, printing the danger rules' error, which names the
// rule's line in risk.toml, and never gets as far as status.json.
func TestServeRefusesBadRiskOverride(t *testing.T) {
	dir, _ := serveHome(t)
	path := filepath.Join(dir, "risk.toml")
	if err := os.WriteFile(path, []byte(badRules), 0o600); err != nil {
		t.Fatal(err)
	}
	_, want := risk.Load(path)
	if want == nil {
		t.Fatal("risk.Load accepts the bad rules")
	}

	stdout, stderr, code := pagerBridgeWithin(t, 10*time.Second, "serve")
	t.Logf("serve: exit %d, stderr %q", code, stderr)
	switch {
	case code == -1:
		t.Fatalf("serve was still running after 10 s; stderr %q", stderr)
	case code == 0 || stdout != "" || !strings.Contains(stderr, want.Error()):
		t.Errorf("exit %d, stdout %q, stderr %q; want non-zero, nothing on stdout, and the rules' error %q", code, stdout, stderr, want)
	}
	if _, err := os.Stat(filepath.Join(dir, status.File)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("status.json: %v; want none, as serve never started", err)
	}

	m := regexp.MustCompile(`risk\.toml:(\d+)`).FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("stderr %q names no line of risk.toml", stderr)
	}
	if n, _ := strconv.Atoi(m[1]); n < 8 || n > 12 {
		t.Errorf("stderr names risk.toml line %d; want a line of the failing rule, 8 to 12", n)
	}
}

// TestServeExitsOnSigterm checks parent 11.1 and 1.4 (S2-5). serve, run as
// the test's own user, binds its port itself and answers on it with its own
// SSH server (Go's, not the system's sshd, so no Remote Login and no root);
// it serves a paired device. On SIGTERM, and on SIGINT while a poll of herdr
// is stuck in flight, it closes the device's session and exits 0 within 2 s.
// TestServeIdleTimeoutFromConfig checks that config.toml's idle_timeout
// reaches serve's sessions (the parent design's config.toml; docs/bridge.md
// §Settings): with "1s", a device
// that says hello and then nothing has its session closed after a second,
// and the log says so with the time. The e2e tests build their sessions
// themselves, so this is the one test of serve's wiring of it.
func TestServeIdleTimeoutFromConfig(t *testing.T) {
	dir, socket := serveHome(t)
	cfg, err := os.OpenFile(filepath.Join(dir, "config.toml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.WriteString("idle_timeout = \"1s\"\n"); err != nil {
		t.Fatal(err)
	}
	cfg.Close()
	fake, err := herdrtest.Start(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fake.Close() })
	kind := "claude"
	fake.SetAgents([]herdr.AgentInfo{{TerminalID: "term_a", PaneID: "w1:p1", Agent: &kind, Status: "working", StateChangeSeq: 1}})
	host, keys := pairServe(t, dir, "pager-01")
	p := startServe(t)

	dev, err := devsim.Dial(p.addr, keys[0], host)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	dev.Send(`{"t":"hello","v":1,"fw":"0.1.0","name":"pager-01"}`)
	began := time.Now()
	for {
		_, err := dev.Recv(5 * time.Second)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || time.Since(began) > 10*time.Second {
			t.Fatalf("the silent device's session after %v: %v; want it closed after 1 s (EOF)", time.Since(began).Round(time.Millisecond), err)
		}
	}
	if took := time.Since(began); took > 3*time.Second {
		t.Errorf("the silent device's session closed after %v; want about 1 s", took.Round(time.Millisecond))
	}
	// The line reaches the pipe from serve's stderr after the close reaches
	// the device, so it's waited for.
	for deadline := time.Now().Add(2 * time.Second); !strings.Contains(p.log.String(), `msg="session: idle; session closed" after=1s`); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Errorf("serve didn't log the idle close after 1 s; stderr:\n%s", p.log.String())
			break
		}
	}
}

// TestServeStatusSaysDevicesWontLoad: while devices.toml won't load, serve
// pairs no device (open-items O-51, failing closed), and `status` says why,
// in its text and as status.json's devices_error.
func TestServeStatusSaysDevicesWontLoad(t *testing.T) {
	dir, _ := serveHome(t)
	pairServe(t, dir, "pager-01")
	startServe(t)
	tmp := filepath.Join(dir, ".devices.toml.tmp")
	if err := os.WriteFile(tmp, []byte("[[device]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "devices.toml")); err != nil {
		t.Fatal(err)
	}
	var out string
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		out, _, _ = pagerBridgeWithin(t, 5*time.Second, "status", "--json")
		if strings.Contains(out, `"devices_error"`) || time.Now().After(deadline) {
			break
		}
	}
	if !strings.Contains(out, `"devices":[]`) || !strings.Contains(out, `"devices_error":"`) {
		t.Fatalf("status --json with devices.toml broken: %s; want no devices, and devices_error", out)
	}
	text, _, code := pagerBridgeWithin(t, 5*time.Second, "status")
	if code != 0 || !strings.Contains(text, "devices.toml won't load") || !strings.Contains(text, "no device can connect") {
		t.Errorf("status with devices.toml broken (exit %d):\n%s", code, text)
	}
}

func TestServeExitsOnSigterm(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("the tests run as root; serve must work as a user (parent 1.4), so run them as one")
	}
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			dir, socket := serveHome(t)
			fake, err := herdrtest.Start(socket)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { fake.Close() })
			kind := "claude"
			fake.SetAgents([]herdr.AgentInfo{{TerminalID: "term_a", PaneID: "w1:p1", Agent: &kind, Status: "working", StateChangeSeq: 1}})
			host, keys := pairServe(t, dir, "pager-01")
			p := startServe(t)

			conn, err := net.DialTimeout("tcp", p.addr, 5*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			banner, err := bufio.NewReader(conn).ReadString('\n')
			conn.Close()
			if !strings.HasPrefix(banner, "SSH-2.0-Go") {
				t.Errorf("%s answers %q, %v; want serve's own SSH server, SSH-2.0-Go", p.addr, banner, err)
			}

			dev, err := devsim.Dial(p.addr, keys[0], host)
			if err != nil {
				t.Fatal(err)
			}
			defer dev.Close()
			dev.Send(`{"t":"hello","v":1,"fw":"0.1.0","name":"pager-01"}`)
			for _, want := range []string{`{"t":"hello"`, `{"t":"snap"`} {
				if line, err := dev.Recv(5 * time.Second); err != nil || !strings.HasPrefix(line, want) {
					t.Fatalf("the device got %q, %v; want %s…", line, err, want)
				}
			}

			if sig == syscall.SIGINT {
				polls := len(fake.Methods())
				fake.Hang(true) // the next poll gets no answer
				for deadline := time.Now().Add(5 * time.Second); len(fake.Methods()) == polls; time.Sleep(10 * time.Millisecond) {
					if time.Now().After(deadline) {
						t.Fatal("serve didn't poll herdr again")
					}
				}
			}
			sent := time.Now()
			if err := p.cmd.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			select {
			case <-p.done:
			case <-time.After(5 * time.Second):
				t.Fatalf("serve was still running 5 s after %v; stderr:\n%s", sig, p.log.String())
			}
			if took := time.Since(sent); p.code != 0 || took > 2*time.Second {
				t.Errorf("serve exited %d, %v after %v; want 0 within 2 s; stderr:\n%s", p.code, took.Round(time.Millisecond), sig, p.log.String())
			} else {
				t.Logf("serve exited 0, %v after %v", took.Round(time.Millisecond), sig)
			}
			if _, err := dev.Recv(time.Second); !errors.Is(err, io.EOF) {
				t.Errorf("the device's session after serve exited: %v; want it closed (EOF)", err)
			}
		})
	}
}
