package setup

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestThePageSaysWhatTheCheckpointMissed is the page's side of the owner's
// checkpoint 7.2 (2026-11-07), read from its script (there's no browser in
// the suite):
//   - (2) the flash step's numbered steps, and Update in Manage, say what
//     Chrome's prompt asks and what it calls the board;
//   - (3) a name taken by another key offers Replace, after a confirm, or
//     another name, sending the old key's fingerprint back as replace;
//   - (4) Run at login asks consent for the launchd agent, and shows the
//     command that starts it now;
//   - (5) the first page says no bridge is running, with the exact command
//     the server gives (serve), rather than waiting.
func TestThePageSaysWhatTheCheckpointMissed(t *testing.T) {
	data, err := os.ReadFile("web/setup.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(data)
	fn := func(name string) string {
		i := strings.Index(js, "function "+name+"(")
		if i < 0 {
			t.Fatalf("setup.js has no function %s", name)
		}
		rest := js[i+1:]
		if j := regexp.MustCompile(`\n(async )?function `).FindStringIndex(rest); j != nil {
			rest = rest[:j[0]]
		}
		return rest
	}
	type words struct {
		where, body string
		says        []string
	}
	for _, c := range []words{
		{"pickPort", fn("pickPort"), []string{"to access your serial ports?", "USB JTAG/serial debug unit"}},
		{"tplFlash's numbered steps", fn("tplFlash")[strings.Index(fn("tplFlash"), "class: 'howto'"):], []string{"pickPort()"}},
		{"Manage's Update", fn("updateBlock"), []string{"pickPort()"}},
		{"pairStart", fn("pairStart"), []string{"replace", "e.data.taken"}},
		{"takenBlock", fn("takenBlock"), []string{"window.confirm", "pairStart(t.fingerprint)", "Choose another name"}},
		{"tplPair", fn("tplPair"), []string{"takenBlock()"}},
		{"setLogin", fn("setLogin"), []string{"launchd: !!launchd", "e.data.launchd", "r.commands"}},
		{"loginBlock", fn("loginBlock"), []string{"Use a launchd agent", "setLogin(true, true)", "cmdList(S.login.cmds)"}},
		{"loadDevices", fn("loadDevices"), []string{"d.serve"}},
		{"tplFirst", fn("tplFirst"), []string{"NO BRIDGE RUNNING", "No bridge is running", "cmdList([S.serve"}},
	} {
		for _, s := range c.says {
			if !strings.Contains(c.body, s) {
				t.Errorf("%s doesn't say %q", c.where, s)
			}
		}
	}
}

// jsFunction is the source of setup.js's function name, up to the next
// top-level function.
func jsFunction(t *testing.T, js, name string) string {
	t.Helper()
	i := strings.Index(js, "function "+name+"(")
	if i < 0 {
		t.Fatalf("setup.js has no function %s", name)
	}
	rest := js[i+1:]
	if j := regexp.MustCompile(`\n(async )?function `).FindStringIndex(rest); j != nil {
		rest = rest[:j[0]]
	}
	return rest
}

// TestThePageOffersToChangeTheNetworks is the page's side of the owner's
// recheck of checkpoint 7.2 (2026-11-07), read from its script:
//   - (A) after pairing, the page asks the bridge how the pager's join went
//     (POST /api/pair/join) and says so; when it didn't join, on the first
//     page while the pager hasn't called, and in Manage, it offers Change
//     the Wi-Fi networks: the steps on the pager, the name kept, the
//     networks' names filled in and their passwords asked again;
//   - (C) when devices.toml lists any pager, the flash step warns above its
//     Install button that a first install erases the pager's pairing, and
//     asks before it starts one.
func TestThePageOffersToChangeTheNetworks(t *testing.T) {
	data, err := os.ReadFile("web/setup.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(data)
	for _, c := range []struct {
		where string
		says  []string
	}{
		{"pairConfirm", []string{"watchJoin()"}},
		{"watchJoin", []string{"'/api/pair/join'", "r.result", "r.says"}},
		{"joinBlock", []string{"S.join.says", "connected()", "Change the Wi-Fi networks", "changeNets(pagerName())"}},
		{"tplPair", []string{"joinBlock()"}},
		{"tplFirst", []string{"Change the Wi-Fi networks", "changeNets(d.name)"}},
		{"changeNets", []string{"d.networks", "pass: ''", "pairAgain(name)"}},
		{"devRow", []string{"Change the Wi-Fi networks", "changeNets(d.name)"}},
		{"tplWifi", []string{"PAIR AGAIN", "keyName('BOOT')", "type each password again"}},
		{"pagersPaired", []string{"S.devices"}},
		{"eraseWarning", []string{"pagersPaired()", "This erases the pager and forgets its pairing. To update it and keep the pairing, use Update in Manage."}},
		{"flasher", []string{"'/firmware/manifest.json'", "pagersPaired()", "window.confirm", "stopPropagation()"}},
	} {
		body := jsFunction(t, js, c.where)
		for _, s := range c.says {
			if !strings.Contains(body, s) {
				t.Errorf("%s doesn't say %q", c.where, s)
			}
		}
	}
	// The warning is above the Install button, not under it with the steps.
	flash := jsFunction(t, js, "tplFlash")
	if w, a := strings.Index(flash, "eraseWarning()"), strings.Index(flash, "    action,"); w < 0 || a < 0 || w > a {
		t.Errorf("tplFlash shows eraseWarning() at %d, its action (the Install button) at %d; want the warning first", w, a)
	}
}

// TestThePageLeadsPairedPagersToManage is the page's side of the owner's
// second recheck of checkpoint 7.2 (2026-11-07), read from its script:
//   - (D) when the launchd agent runs another bridge (the doctor's login
//     state "other"), Run at login says which, offers to switch the agent to
//     this bridge (the person's click is the consent), and then gives the
//     commands that reload the job;
//   - (E) when devices.toml lists any pager, the page opens on Manage, and
//     Check the Mac and the Flash step lead there (Update keeps the pairing;
//     Change the Wi-Fi networks; revoke) rather than to a first install,
//     which becomes an explicit choice for a new or reset pager.
func TestThePageLeadsPairedPagersToManage(t *testing.T) {
	data, err := os.ReadFile("web/setup.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(data)
	for _, c := range []struct {
		where string
		says  []string
	}{
		// (D)
		{"loginBlock", []string{"c.state === 'other'", "Switch to this bridge", "setLogin(true, true)", "S.login.offer.current", "reload"}},
		// (E)
		{"start", []string{"pagersPaired()", "setView('manage')"}},
		{"firstInstall", []string{"pagersPaired()", "S.newPager"}},
		{"newPager", []string{"S.newPager = true", "setView('setup')", "go('flash')"}},
		{"pairedLead", []string{"setView('manage')", "Update", "keeps", "Change the Wi-Fi networks", "revoke", "new or reset pager", "newPager()"}},
		{"tplMac", []string{"firstInstall()", "pairedLead()"}},
		{"tplFlash", []string{"firstInstall()", "pairedLead()"}},
		{"foot", []string{"firstInstall()"}},
		{"tplManage", []string{"new or reset pager", "newPager()"}},
	} {
		body := jsFunction(t, js, c.where)
		for _, s := range c.says {
			if !strings.Contains(body, s) {
				t.Errorf("%s doesn't say %q", c.where, s)
			}
		}
	}
	// With a pager paired, the flash step offers no first install until the
	// person chooses one: the lead comes before, and instead of, the
	// flasher for the first install.
	flash := jsFunction(t, js, "tplFlash")
	lead, install := strings.Index(flash, "!firstInstall()"), strings.Index(flash, "flasher('/firmware/manifest.json', ok ?")
	if lead < 0 || install < 0 || lead > install {
		t.Errorf("tplFlash tests !firstInstall() at %d, offers the first install at %d; want the test first", lead, install)
	}
}

// TestThePageSaysWhatTheLastRecheckMissed is open-items O-96, from the
// owner's last recheck of 7.2 (rc.5, 2026-11-07), read from the script:
//   - Manage's Update: ESP Web Tools names every flash "Install herdr-pager"
//     and then asks whether to erase, so the page says what its dialog shows,
//     and how to tell a ticked box from its last screen;
//   - after Switch to this bridge, launchd runs the old bridge until the two
//     commands are run: that's a step still to do, not a note;
//   - pairing a paired pager again: start on the page, then Pair again on the
//     pager, within the page's 2 minutes (the page's banner said "first",
//     and the owner's first try ran out of time).
func TestThePageSaysWhatTheLastRecheckMissed(t *testing.T) {
	data, err := os.ReadFile("web/setup.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(data)
	fn := func(name string) string {
		i := strings.Index(js, "function "+name+"(")
		if i < 0 {
			t.Fatalf("setup.js has no function %s", name)
		}
		rest := js[i+1:]
		if j := regexp.MustCompile(`\n(async )?function `).FindStringIndex(rest); j != nil {
			rest = rest[:j[0]]
		}
		return rest
	}
	for _, c := range []struct {
		where, body string
		says, never []string
	}{
		{"updateBlock", fn("updateBlock"),
			[]string{"Install herdr-pager", "Erase device", "All data on the device will be erased", "go Back"}, nil},
		{"loginBlock", fn("loginBlock"),
			[]string{"S.login.reload ? h('div', { class: 'result need' }", "text: 'RELOAD'", "still runs the bridge it started before"}, nil},
		{"tplPair", fn("tplPair"),
			[]string{"2 minutes", "found at once"}, []string{"in its menu first"}},
	} {
		for _, s := range c.says {
			if !strings.Contains(c.body, s) {
				t.Errorf("%s doesn't say %q", c.where, s)
			}
		}
		for _, s := range c.never {
			if strings.Contains(c.body, s) {
				t.Errorf("%s still says %q", c.where, s)
			}
		}
	}
}
