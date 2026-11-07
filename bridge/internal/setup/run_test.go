package setup

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestSetupRunOpensItsOwnPage is `pager-bridge setup` (task 3.11,
// requirement 2.1): Run serves the page on 127.0.0.1, opens its own address
// and nothing else (the address with the token, which the guard trades for
// the page's cookie), prints that address for a browser that didn't open,
// and stops on Ctrl-C (its context ending).
func TestSetupRunOpensItsOwnPage(t *testing.T) {
	type opened struct {
		port  int
		token string
	}
	run := func(t *testing.T, openErr error) (out *syncBuffer, got opened, stop func() error) {
		t.Helper()
		out = &syncBuffer{}
		calls := make(chan opened, 2)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- Run(ctx, RunOptions{Out: out, Open: func(port int, token string) error {
				calls <- opened{port, token}
				return openErr
			}})
		}()
		select {
		case got = <-calls:
		case err := <-done:
			cancel()
			t.Fatalf("Run ended before it opened the page: %v; it printed:\n%s", err, out)
		case <-time.After(10 * time.Second):
			cancel()
			t.Fatalf("Run didn't open the page within 10 s; it printed:\n%s", out)
		}
		stop = func() error {
			cancel()
			select {
			case err := <-done:
				return err
			case <-time.After(10 * time.Second):
				t.Fatal("Run didn't stop within 10 s of Ctrl-C")
				return nil
			}
		}
		t.Cleanup(func() { cancel() })
		if len(calls) > 0 {
			t.Errorf("Run opened the page more than once: %v", <-calls)
		}
		return out, got, stop
	}

	t.Run("it opens its own address, prints it, and stops on Ctrl-C", func(t *testing.T) {
		out, got, stop := run(t, nil)
		addr := pageURL(got.port, got.token)
		if want := "http://127.0.0.1:" + strconv.Itoa(got.port) + "/?t=" + got.token; addr != want {
			t.Errorf("pageURL(%d, token) = %q; want %q", got.port, addr, want)
		}
		u, err := url.Parse(addr)
		if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() != strconv.Itoa(got.port) || u.Path != "/" || u.Query().Get("t") != got.token || len(u.Query()) != 1 {
			t.Errorf("the page's address %q (%v); want http://127.0.0.1:<port>/ and only its token", addr, err)
		}
		if len(got.token) < 43 || got.port <= 0 {
			t.Errorf("opened port %d with token %q; want the listener's port and the page's 32-byte token", got.port, got.token)
		}
		// Printed, for a browser that didn't open, with how long it lasts.
		waitPrinted(t, out, addr)
		waitPrinted(t, out, "5 minutes")
		waitPrinted(t, out, "Ctrl-C")

		// The address it opened is the page: the token, traded for the cookie.
		jar, _ := cookiejar.New(nil)
		client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
		resp, err := client.Get(addr)
		if err != nil {
			t.Fatal(err)
		}
		page, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || !bytes.Contains(page, []byte("<title>herdr-pager setup</title>")) || resp.Request.URL.RawQuery != "" {
			t.Errorf("GET %s: %d at %s, %.60q; want the page at /, the token gone from the address", addr, resp.StatusCode, resp.Request.URL, page)
		}
		// The same port without the token is refused.
		if resp, err := http.Get("http://127.0.0.1:" + strconv.Itoa(got.port) + "/"); err != nil || resp.StatusCode != http.StatusForbidden {
			t.Errorf("GET / with no token: %v %v; want 403", resp, err)
		}

		if err := stop(); err != nil {
			t.Errorf("Run after Ctrl-C: %v; want nil", err)
		}
		if c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(got.port), time.Second); err == nil {
			c.Close()
			t.Errorf("port %d still takes connections after Ctrl-C", got.port)
		}
		waitPrinted(t, out, "stopped")
	})

	t.Run("a browser that won't open: the address is printed, and the page serves", func(t *testing.T) {
		out, got, stop := run(t, errors.New("no browser"))
		addr := pageURL(got.port, got.token)
		waitPrinted(t, out, addr)
		waitPrinted(t, out, "no browser")
		jar, _ := cookiejar.New(nil)
		resp, err := (&http.Client{Jar: jar, Timeout: 5 * time.Second}).Get(addr)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s after open failed: %v %v; want the page", addr, resp, err)
		}
		if err := stop(); err != nil {
			t.Errorf("Run after Ctrl-C: %v; want nil", err)
		}
	})
}

// TestSetupMain is `pager-bridge setup`'s flags (Main, through mainWith's
// run, so nothing is served or opened): an argument or an unknown flag is
// exit 2 and runs nothing; otherwise Run gets this Mac's HOME, the firmware
// (--firmware made absolute, or the one beside the executable), the
// bridge's log files, and the terminal for the address and the step lines.
func TestSetupMain(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	main := func(args ...string) (code int, stdout, stderr string, got *RunOptions) {
		var out, errOut bytes.Buffer
		code = mainWith(context.Background(), &out, &errOut, args, func(ctx context.Context, o RunOptions) error {
			got = &o
			return nil
		})
		return code, out.String(), errOut.String(), got
	}
	for _, args := range [][]string{{"extra"}, {"--nope"}, {"--firmware"}} {
		if code, _, stderr, got := main(args...); code != 2 || got != nil || stderr == "" {
			t.Errorf("setup %q: exit %d, ran %v, stderr %q; want exit 2, nothing run, and why", args, code, got != nil, stderr)
		}
	}
	code, _, _, got := main("--firmware", "firmware/build")
	wd, _ := os.Getwd()
	if code != 0 || got == nil || got.Firmware != filepath.Join(wd, "firmware/build") {
		t.Fatalf("setup --firmware firmware/build: exit %d, options %+v; want 0 and the folder made absolute", code, got)
	}
	if got.Home != home || got.Getenv == nil || got.LocalHostName == nil || got.KnownNetworks == nil || got.Open != nil {
		t.Errorf("setup gives Run %+v; want HOME %s, the Mac's environment, name and networks, and the real open", got.Options, home)
	}
	if strings.Join(got.BridgeLogs, " ") != strings.Join(BridgeLogs(home), " ") || got.Out == nil || got.Log == nil {
		t.Errorf("setup gives Run the logs %v, Out %v, Log %v; want BridgeLogs(HOME) and the terminal", got.BridgeLogs, got.Out, got.Log)
	}
	exe, _ := os.Executable()
	if _, _, _, got := main(); got == nil || got.Firmware != DefaultFirmware(exe) {
		t.Errorf("setup with no --firmware gives Run %v; want DefaultFirmware(%s)", got, exe)
	}
	if code, _, stderr, _ := func() (int, string, string, *RunOptions) {
		var out, errOut bytes.Buffer
		c := mainWith(context.Background(), &out, &errOut, nil, func(context.Context, RunOptions) error { return errors.New("can't listen") })
		return c, out.String(), errOut.String(), nil
	}(); code != 1 || !strings.Contains(stderr, "can't listen") {
		t.Errorf("setup whose Run fails: exit %d, stderr %q; want exit 1 and why", code, stderr)
	}
}

// waitPrinted waits up to 5 s for out to hold s.
func waitPrinted(t *testing.T, out *syncBuffer, s string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(out.String(), s); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("Run never printed %q; it printed:\n%s", s, out)
		}
	}
}
