package main

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devsim"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdrtest"
)

// A stdioProc is stdio running as a process: its stdin, and its stdout line
// by line.
type stdioProc struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	lines chan string   // stdout's lines, closed when it ends
	done  chan struct{} // closed once it has exited
	code  int           // its exit code, once done
	log   serveLog      // its stderr
}

// startStdio starts `pager-bridge stdio` as a process in the test's HOME
// (bridgeCommand); it's killed when the test ends, if it's still running.
func startStdio(t *testing.T) *stdioProc {
	t.Helper()
	p := &stdioProc{cmd: bridgeCommand(t, "stdio"), lines: make(chan string, 64), done: make(chan struct{})}
	p.cmd.Stderr = &p.log
	var err error
	if p.stdin, err = p.cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		r := bufio.NewReader(stdout)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				break
			}
			p.lines <- strings.TrimSuffix(line, "\n")
		}
		close(p.lines)
		p.cmd.Wait()
		p.code = p.cmd.ProcessState.ExitCode()
		close(p.done)
	}()
	t.Cleanup(func() {
		p.cmd.Process.Kill()
		<-p.done
	})
	return p
}

func (p *stdioProc) Send(line string) error {
	_, err := io.WriteString(p.stdin, line+"\n")
	return err
}

// Recv returns stdio's next line, or io.EOF once its stdout has ended.
func (p *stdioProc) Recv(timeout time.Duration) (string, error) {
	select {
	case line, ok := <-p.lines:
		if !ok {
			return "", io.EOF
		}
		return line, nil
	case <-time.After(timeout):
		return "", devsim.ErrTimeout
	}
}

// exited waits up to 5 s for the process to exit, and returns its code, or
// -1 if it hasn't.
func (p *stdioProc) exited() int {
	select {
	case <-p.done:
		return p.code
	case <-time.After(5 * time.Second):
		return -1
	}
}

// A pager is either end of a session the test speaks for a device.
type pager interface {
	Send(line string) error
	Recv(timeout time.Duration) (string, error)
}

// times are the fields that hold the bridge's clock, masked to compare
// replies.
var times = regexp.MustCompile(`"(now|since)":\d+`)

// converse has one device's conversation with the bridge: hello, then once
// the bridge's hello and first snapshot are in, a ping, a list, a show, a
// message of an unknown type (no reply) and a ping. It returns the bridge's
// six replies, with their times masked.
func converse(t *testing.T, d pager) []string {
	t.Helper()
	var replies []string
	read := func(n int) {
		t.Helper()
		for range n {
			line, err := d.Recv(5 * time.Second)
			if err != nil {
				t.Fatalf("after %q: %v", replies, err)
			}
			replies = append(replies, times.ReplaceAllString(line, `"$1":0`))
		}
	}
	d.Send(`{"t":"hello","v":1,"fw":"0.1.0","name":"pager-01"}`)
	read(2)
	for _, line := range []string{
		`{"t":"ping","id":1}`,
		`{"t":"list","id":2}`,
		`{"t":"show","id":3,"pane":"w1:p1","pg":-1}`,
		`{"t":"send_text","id":4,"text":"y"}`,
		`{"t":"ping","id":5}`,
	} {
		d.Send(line)
	}
	read(4)
	return replies
}

// TestStdioSession checks parent 11.3: stdio speaks the pager protocol on
// stdin and stdout for one session, as an SSH session does. The same
// conversation gets the same replies from stdio as from serve over SSH, once
// their times are masked; when stdin closes, stdio exits 0; and a 513-byte
// line ends the session (parent 13.5), as it ends an SSH one, and stdio exits
// non-zero.
func TestStdioSession(t *testing.T) {
	dir, socket := serveHome(t)
	fake, err := herdrtest.Start(socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fake.Close() })
	kind := "claude"
	fake.SetAgents([]herdr.AgentInfo{{TerminalID: "term_a", PaneID: "w1:p1", Agent: &kind, Status: "blocked", StateChangeSeq: 3}})
	host, keys := pairServe(t, dir, "pager-01")

	s := startServe(t)
	dev, err := devsim.Dial(s.addr, keys[0], host)
	if err != nil {
		t.Fatal(err)
	}
	defer dev.Close()
	overSSH := converse(t, dev)

	p := startStdio(t)
	overStdio := converse(t, p)
	t.Logf("over stdio:\n%s", strings.Join(overStdio, "\n"))
	if !slices.Equal(overStdio, overSSH) {
		t.Errorf("over stdio:\n%s\nover SSH:\n%s\nwant the same", strings.Join(overStdio, "\n"), strings.Join(overSSH, "\n"))
	}
	if line, err := p.Recv(200 * time.Millisecond); !errors.Is(err, devsim.ErrTimeout) {
		t.Errorf("over stdio, after the conversation: %q, %v; want nothing more", line, err)
	}
	p.stdin.Close()
	if _, err := p.Recv(5 * time.Second); !errors.Is(err, io.EOF) {
		t.Errorf("stdin closed: %v; want stdout to end", err)
	}
	if code := p.exited(); code != 0 {
		t.Errorf("stdin closed: stdio exited %d; want 0; stderr:\n%s", code, p.log.String())
	}

	long, err := os.ReadFile("../../testdata/protocol/to-bridge/invalid/ping-513-bytes.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []pager{dev, startStdio(t)} {
		if d != pager(dev) {
			d.Send(`{"t":"hello","v":1,"fw":"0.1.0","name":"pager-01"}`)
		}
		d.Send(strings.TrimSuffix(string(long), "\n"))
		ended := false
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && !ended; {
			_, err := d.Recv(time.Until(deadline))
			ended = errors.Is(err, io.EOF)
		}
		if !ended {
			t.Errorf("%T: a 513-byte line didn't end the session", d)
		}
		if p, ok := d.(*stdioProc); ok {
			if code := p.exited(); code == 0 || code == -1 {
				t.Errorf("stdio, after a 513-byte line: exit %d; want non-zero", code)
			}
		}
	}
}
