package e2e

import (
	"bufio"
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devsim"
)

// dialRaw makes an SSH connection to the bridge as user, with clientVersion
// and auths, and returns the connection's source address and the error. A
// connection that authenticates is closed at once.
func dialRaw(t *testing.T, b *testBridge, user, clientVersion string, auths ...ssh.AuthMethod) (string, error) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", b.addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	c, _, _, err := ssh.NewClientConn(conn, b.addr, &ssh.ClientConfig{
		User: user, ClientVersion: clientVersion, Auth: auths,
		HostKeyCallback: ssh.FixedHostKey(b.hostKey),
	})
	if err == nil {
		c.Close()
	}
	return conn.LocalAddr().String(), err
}

// TestUnknownKeyRejected checks parent 3.2 and 3.7: a key that isn't a
// paired device's is refused, and the bridge logs its full SHA256
// fingerprint and the source address, and nothing else the client sent: not
// its user name, its version string or its key.
func TestUnknownKeyRejected(t *testing.T) {
	b := startBridge(t, nil)
	b.pair("pager-01")
	stranger := newKey(t)

	addr, err := dialRaw(t, b, "sneaky-user", "SSH-2.0-sneaky-client", ssh.PublicKeys(stranger))
	if err == nil {
		t.Fatal("an unpaired key authenticated")
	}
	log := b.log.String()
	fp := ssh.FingerprintSHA256(stranger.PublicKey())
	if !strings.Contains(log, fp) || !strings.Contains(log, addr) {
		t.Errorf("the log doesn't name the key's fingerprint %s and the address %s:\n%s", fp, addr, log)
	}
	for _, sent := range []string{"sneaky-user", "sneaky-client", base64.StdEncoding.EncodeToString(stranger.PublicKey().Marshal())} {
		if strings.Contains(log, sent) {
			t.Errorf("the log carries %q, which the client sent:\n%s", sent, log)
		}
	}
}

// TestAlteredKeyNeverMatches checks parent 13.8: keys are compared in full.
// A device whose listed key differs from its own in any one bit of the 32
// key bytes is refused, and a devices.toml whose key line is cut short
// doesn't load, so it can't list a key that matches by its prefix. The
// unaltered key, listed, authenticates.
func TestAlteredKeyNeverMatches(t *testing.T) {
	b := startBridge(t, nil)
	key := newKey(t)
	wire := key.PublicKey().Marshal()

	for i := len(wire) - ed25519.PublicKeySize; i < len(wire); i++ {
		for _, bit := range []byte{0x01, 0x80} {
			altered := bytes.Clone(wire)
			altered[i] ^= bit
			pub, err := ssh.ParsePublicKey(altered)
			if err != nil {
				t.Fatal(err)
			}
			b.list(devices.Device{Name: "pager-01", PublicKey: pub, Added: start})
			if _, err := dialRaw(t, b, "pager", "", ssh.PublicKeys(key)); err == nil {
				t.Errorf("the key authenticated against a listed key with byte %d's bit %#x flipped", i, bit)
			}
		}
	}

	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key.PublicKey())))
	path := filepath.Join(t.TempDir(), devices.File)
	for _, cut := range []string{line[:len(line)-4], line[:len("ssh-ed25519 ")+40]} {
		toml := fmt.Sprintf("[[device]]\nname  = \"pager-01\"\nkey   = %q\nadded = 2026-10-22T10:00:00Z\n", cut)
		if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
			t.Fatal(err)
		}
		if devs, err := devices.Load(path); err == nil {
			t.Errorf("a devices.toml with the key cut to %d characters loaded: %+v", len(cut), devs)
		}
	}

	b.list(devices.Device{Name: "pager-01", Key: line, PublicKey: key.PublicKey(), Added: start})
	if _, err := dialRaw(t, b, "pager", "", ssh.PublicKeys(key)); err != nil {
		t.Errorf("the unaltered key: %v; want it to authenticate", err)
	}
}

// TestDevsimDetectsHostKeyChange checks parent 3.3 from the device's side:
// devsim, pinned to the bridge's host key, connects; pinned to another key,
// as after the bridge's key changed, it fails with ErrHostKeyChanged, not as
// an authentication failure.
func TestDevsimDetectsHostKeyChange(t *testing.T) {
	b := startBridge(t, nil)
	key := b.pair("pager-01")
	d, err := devsim.Dial(b.addr, key, b.hostKey)
	if err != nil {
		t.Fatalf("pinned to the bridge's host key: %v", err)
	}
	d.Close()
	if _, err := devsim.Dial(b.addr, key, newKey(t).PublicKey()); !errors.Is(err, devsim.ErrHostKeyChanged) {
		t.Errorf("pinned to another key: %v; want devsim.ErrHostKeyChanged", err)
	}
}

// TestRejectsPtyForwardingSubsystem checks parent 3.8 and parent design
// §sshserver: a channel other than session (direct-tcpip, x11) is refused;
// on the session channel, pty-req, env, subsystem and x11-req are refused;
// tcpip-forward is refused; keepalive@openssh.com is answered false, and the
// session goes on to its exec, hello and snapshot.
func TestRejectsPtyForwardingSubsystem(t *testing.T) {
	b := startBridge(t, nil)
	key := b.pair("pager-01")
	conn, err := net.DialTimeout("tcp", b.addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c, chans, reqs, err := ssh.NewClientConn(conn, b.addr, &ssh.ClientConfig{
		User: "pager", Auth: []ssh.AuthMethod{ssh.PublicKeys(key)}, HostKeyCallback: ssh.FixedHostKey(b.hostKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	client := ssh.NewClient(c, chans, reqs)
	defer client.Close()

	forward := ssh.Marshal(struct {
		Host       string
		Port       uint32
		OriginHost string
		OriginPort uint32
	}{"127.0.0.1", 22, "127.0.0.1", 40000})
	for _, kind := range []string{"direct-tcpip", "x11", "forwarded-tcpip"} {
		if ch, _, err := client.OpenChannel(kind, forward); err == nil {
			ch.Close()
			t.Errorf("a %s channel was accepted", kind)
		}
	}
	if ok, _, err := client.SendRequest("tcpip-forward", true, ssh.Marshal(struct {
		Addr string
		Port uint32
	}{"127.0.0.1", 0})); ok || err != nil {
		t.Errorf("tcpip-forward: %v, %v; want refused", ok, err)
	}

	s, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err == nil {
		t.Error("pty-req was accepted")
	}
	if err := s.Setenv("LANG", "C"); err == nil {
		t.Error("env was accepted")
	}
	if err := s.RequestSubsystem("sftp"); err == nil {
		t.Error("subsystem was accepted")
	}
	x11 := ssh.Marshal(struct {
		Single   bool
		Protocol string
		Cookie   string
		Screen   uint32
	}{false, "MIT-MAGIC-COOKIE-1", "00", 0})
	if ok, err := s.SendRequest("x11-req", true, x11); ok || err != nil {
		t.Errorf("x11-req: %v, %v; want refused", ok, err)
	}
	if ok, _, err := client.SendRequest("keepalive@openssh.com", true, nil); ok || err != nil {
		t.Errorf("keepalive@openssh.com: %v, %v; want reply false, the connection kept", ok, err)
	}

	stdin, _ := s.StdinPipe()
	stdout, _ := s.StdoutPipe()
	if err := s.Start("pager"); err != nil {
		t.Fatalf("exec after the refusals: %v", err)
	}
	fmt.Fprintln(stdin, `{"t":"hello","v":1,"fw":"0.1.0","name":"pager-01"}`)
	r := bufio.NewReader(stdout)
	for _, want := range []string{`{"t":"hello"`, `{"t":"snap"`} {
		line, err := r.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, want) {
			t.Errorf("after the refusals: %q, %v; want %s…", line, err, want)
		}
	}
}

// TestAuthOffersOnlyPublicKey checks parent 3.2 and the threat model's T1:
// the bridge offers no password or keyboard-interactive method; a fourth key
// attempt on one connection is cut off (MaxAuthTries 3); and an rsa or ecdsa
// key is refused even when listed as a device's.
func TestAuthOffersOnlyPublicKey(t *testing.T) {
	b := startBridge(t, nil)
	paired := b.pair("pager-01")

	asked := false
	_, err := dialRaw(t, b, "pager", "",
		ssh.PasswordCallback(func() (string, error) { asked = true; return "secret", nil }),
		ssh.KeyboardInteractive(func(string, string, []string, []bool) ([]string, error) { asked = true; return nil, nil }))
	if err == nil || asked || strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "keyboard-interactive") {
		t.Errorf("password and keyboard-interactive: %v, asked for a secret: %v; want neither offered", err, asked)
	}

	if _, err := dialRaw(t, b, "pager", "", ssh.PublicKeys(newKey(t), newKey(t), paired)); err != nil {
		t.Errorf("two unknown keys, then the paired one: %v; want the third try to authenticate", err)
	}
	if _, err := dialRaw(t, b, "pager", "", ssh.PublicKeys(newKey(t), newKey(t), newKey(t), paired)); err == nil {
		t.Error("three unknown keys, then the paired one: authenticated; want the fourth try cut off")
	}

	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []crypto.Signer{ecdsaKey, rsaKey} {
		s, err := ssh.NewSignerFromSigner(k)
		if err != nil {
			t.Fatal(err)
		}
		b.pairKey("other-"+s.PublicKey().Type(), s)
		if _, err := dialRaw(t, b, "pager", "", ssh.PublicKeys(s)); err == nil {
			t.Errorf("a listed %s key authenticated; device keys are ssh-ed25519", s.PublicKey().Type())
		}
	}
}
