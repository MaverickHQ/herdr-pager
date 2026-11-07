package setup

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestRunAtLoginWithoutHomebrew is the owner's checkpoint 7.2 (2026-11-07),
// items 4 and 5: from an archive unpacked by hand (the Without Homebrew
// route), Run at login ran `brew services start herdr-pager`, which said "No
// available formula with the name herdr-pager", and then nothing served the
// bridge, while the page's hint named `pager-bridge serve`, which isn't on
// PATH there. A bridge not installed by Homebrew runs at login by the launchd
// agent (contrib/launchd's, with this executable's path), written only once
// the page has the person's consent; and the page names the exact command
// that serves this bridge.
func TestRunAtLoginWithoutHomebrew(t *testing.T) {
	var exe string
	p := newPairPageWith(t, func(o *Options) {
		exe = filepath.Join(o.Home, "herdr pager trial", "herdr-pager-1.0.0-rc.1", "bin", "pager-bridge")
		o.Executable = exe
		o.RunBrew = func(argv ...string) ([]byte, error) {
			t.Errorf("ran %q for a bridge Homebrew didn't install", argv)
			return []byte("Error: No available formula with the name herdr-pager\n"), &os.PathError{Op: "brew", Path: "herdr-pager", Err: os.ErrInvalid}
		}
	})
	plist := filepath.Join(p.home, "Library", "LaunchAgents", "dev.herdr-pager.bridge.plist")
	quoted := "'" + exe + "'"

	status, v := p.get("/api/devices")
	if status != http.StatusOK || field(v, "serve") != quoted+" serve" {
		t.Errorf("GET /api/devices: %d, serve %q; want %q", status, field(v, "serve"), quoted+" serve")
	}

	// Without consent: the page is told what the launchd agent would run,
	// and nothing is written.
	status, v = p.post("/api/login", map[string]bool{"on": true})
	if status != http.StatusConflict || field(v, "launchd.program") != exe || field(v, "launchd.plist") != plist {
		t.Errorf("POST /api/login {on}: %d %v; want 409 offering the launchd agent for %s at %s", status, v, exe, plist)
	}
	if _, err := os.Stat(plist); err == nil {
		t.Fatalf("POST /api/login {on} wrote %s without consent", plist)
	}

	// With consent: the agent, with this executable's path.
	status, v = p.post("/api/login", map[string]bool{"on": true, "launchd": true})
	if status != http.StatusOK {
		t.Fatalf("POST /api/login {on, launchd}: %d %v; want 200", status, v)
	}
	data, err := os.ReadFile(plist)
	if err != nil {
		t.Fatalf("POST /api/login {on, launchd}: %v", err)
	}
	for _, want := range []string{"<string>dev.herdr-pager.bridge</string>", "<string>" + exe + "</string>", "<string>serve</string>",
		"<string>" + p.home + "</string>", "<string>" + p.home + "/Library/Logs/herdr-pager/bridge.log</string>", "<key>RunAtLoad</key>"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("%s doesn't hold %s:\n%s", plist, want, data)
		}
	}
	if cmds := anyStrings(v["commands"]); !slices.ContainsFunc(cmds, func(c string) bool { return strings.HasPrefix(c, "launchctl bootstrap gui/$(id -u) ") }) {
		t.Errorf("POST /api/login {on, launchd}: commands %q; want launchctl bootstrap, to start it now", cmds)
	}

	// Off: the agent goes.
	status, v = p.post("/api/login", map[string]bool{"on": false})
	if _, err := os.Stat(plist); status != http.StatusOK || err == nil {
		t.Errorf("POST /api/login {off}: %d %v, the plist still there: %v", status, v, err == nil)
	}
	if cmds := anyStrings(v["commands"]); !slices.Contains(cmds, "launchctl bootout gui/$(id -u)/dev.herdr-pager.bridge") {
		t.Errorf("POST /api/login {off}: commands %q; want launchctl bootout", cmds)
	}
}

// TestRunAtLoginSwitchesTheAgentToThisBridge is the owner's second recheck of
// checkpoint 7.2 (2026-11-07), item D: rc.3's page wrote the launchd agent,
// and rc.4's page and doctor said Run at login was on while launchd ran
// rc.3's serve. Asked to run this bridge at login, the page says which bridge
// the agent runs now and changes nothing until the person agrees; then it
// rewrites the agent for this executable, and gives the commands that reload
// the job, bootout before bootstrap, since bootstrap alone fails while the old
// job is loaded.
func TestRunAtLoginSwitchesTheAgentToThisBridge(t *testing.T) {
	var rc3, rc4 string
	p := newPairPageWith(t, func(o *Options) {
		rc3 = filepath.Join(o.Home, "herdr-pager-trial", "herdr-pager-1.0.0-rc.3", "bin", "pager-bridge")
		rc4 = filepath.Join(o.Home, "herdr-pager-trial", "herdr-pager-1.0.0-rc.4", "bin", "pager-bridge")
		o.Executable = rc4
		o.RunBrew = func(argv ...string) ([]byte, error) {
			t.Errorf("ran %q for a bridge Homebrew didn't install", argv)
			return nil, os.ErrInvalid
		}
	})
	plist := filepath.Join(p.home, "Library", "LaunchAgents", "dev.herdr-pager.bridge.plist")
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		t.Fatal(err)
	}
	rc3Agent := fmt.Sprintf(launchdPlist, rc3, p.home) // as rc.3's page wrote it
	if err := os.WriteFile(plist, []byte(rc3Agent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without consent: which bridge it runs now, and which it would run.
	status, v := p.post("/api/login", map[string]bool{"on": true})
	if status != http.StatusConflict || field(v, "launchd.current") != rc3 || field(v, "launchd.program") != rc4 || !strings.Contains(field(v, "error"), rc3) {
		t.Errorf("POST /api/login {on}, the agent running rc.3: %d %v; want 409 saying it runs %s, offering %s", status, v, rc3, rc4)
	}
	if data, _ := os.ReadFile(plist); string(data) != rc3Agent {
		t.Fatalf("POST /api/login {on} changed %s without consent:\n%s", plist, data)
	}

	// With consent: the agent runs this bridge, and the job is reloaded.
	status, v = p.post("/api/login", map[string]bool{"on": true, "launchd": true})
	if status != http.StatusOK {
		t.Fatalf("POST /api/login {on, launchd}: %d %v; want 200", status, v)
	}
	data, err := os.ReadFile(plist)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<string>"+rc4+"</string>") || strings.Contains(string(data), rc3) {
		t.Errorf("%s after the switch; want it running %s, not %s:\n%s", plist, rc4, rc3, data)
	}
	want := []string{"launchctl bootout gui/$(id -u)/dev.herdr-pager.bridge", "launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.herdr-pager.bridge.plist"}
	if cmds := anyStrings(v["commands"]); !slices.Equal(cmds, want) {
		t.Errorf("POST /api/login {on, launchd}, switching: commands %q; want %q, to reload the job now", cmds, want)
	}
}
