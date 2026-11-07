package e2e

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdrtest"
)

// allowed are the only methods the bridge may call on herdr (parent 13.2),
// written out here rather than taken from the herdr client.
var allowed = []string{"agent.list", "agent.get", "agent.read", "agent.send_keys", "agent.focus", "tab.list", "workspace.list"}

// TestMain gives the suite a temporary HOME and no HERDR_SOCKET_PATH, so no
// test can reach the owner's config or herdr; each test then starts its own
// fake herdr on a socket in a short temp folder (startBridge). Once every
// test has run, it fails the suite if any fake herdr was called with a method
// outside the allowed ones (TestMainAssertsAllowedMethodsOnly), or if any log
// the suite captured from a bridge carries an agent's name or title, a line of
// its pane text or a key (TestLogsCarryNoAgentText).
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "hp")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	os.Unsetenv("HERDR_SOCKET_PATH")
	code := m.Run()
	os.RemoveAll(home)
	if bad := disallowed(herdrtest.AllMethods()); len(bad) > 0 {
		fmt.Fprintf(os.Stderr, "--- FAIL: TestMainAssertsAllowedMethodsOnly\n    herdr was called with %q, outside the methods parent 13.2 allows\n", bad)
		code = 1
	}
	if found := leaks(suiteLogs(), needles()); len(found) > 0 {
		fmt.Fprintf(os.Stderr, "--- FAIL: TestLogsCarryNoAgentText\n    the bridge logged agent text or a key %d times (parent 11.5, 3.7):\n", len(found))
		for _, f := range found[:min(len(found), 20)] {
			fmt.Fprintln(os.Stderr, "    "+f)
		}
		code = 1
	}
	os.Exit(code)
}

// disallowed returns the methods outside allowed, once each.
func disallowed(methods []string) []string {
	var bad []string
	for _, m := range methods {
		if !slices.Contains(allowed, m) && !slices.Contains(bad, m) {
			bad = append(bad, m)
		}
	}
	return bad
}

// TestMainAssertsAllowedMethodsOnly checks what TestMain's check rests on:
// every request to any fake herdr started in this process, however it was
// started, is in herdrtest.AllMethods, even after the server has closed; and
// disallowed flags any method but the five. (TestMain makes the check itself,
// after every test of the package has run.)
func TestMainAssertsAllowedMethodsOnly(t *testing.T) {
	dir, err := os.MkdirTemp("", "hp")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	fake, err := herdrtest.Start(filepath.Join(dir, "herdr.sock"))
	if err != nil {
		t.Fatal(err)
	}
	before := len(herdrtest.AllMethods())
	conn, err := net.DialTimeout("unix", fake.Socket, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(conn, `{"id":"t","method":"agent.focus","params":{"target":"w1:p1"}}`)
	bufio.NewReader(conn).ReadString('\n')
	conn.Close()
	fake.Close()
	if all := herdrtest.AllMethods(); len(all) != before+1 || all[len(all)-1] != "agent.focus" {
		t.Errorf("after one agent.focus to a fake herdr, the suite's record went from %d to %d methods: %q", before, len(all), all)
	}

	if bad := disallowed(append(slices.Clone(allowed), "pane.send_text", "agent.prompt", "pane.send_input", "agent.respond", "")); !slices.Equal(bad, []string{"pane.send_text", "agent.prompt", "pane.send_input", "agent.respond", ""}) {
		t.Errorf("disallowed flagged %q", bad)
	}
}
