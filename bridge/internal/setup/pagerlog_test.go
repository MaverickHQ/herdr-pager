package setup

import (
	"net/http"
	"strings"
	"testing"
)

// TestThePagersLogOnThePage is the page's pager channel (requirement 2.2,
// design §The setup page, Live output): while the bridge holds the pager's
// port to pair, the pager's own serial lines (provision.Options.Log, task
// 3.6) reach every tab as text, with terminal colours and other control
// characters taken out; its provisioning lines never do, and nor does a
// line holding a password, such as a console's echo of the prov_set.
func TestThePagersLogOnThePage(t *testing.T) {
	backlog := "\x1b[0;32mI (31) pager: in colour\x1b[0m\r\n" +
		"rst:0x1 (POWERON)\tboot:0x8\x07\x00\r\n"
	pager := newFakePagerWith(t, "to-tool/valid/prov_result-ok.txt", backlog, "> ")
	p := newPairPage(t, pager.tty)
	p.pair(pager.tty)

	p.tab.wait(t, chanPager, "I (1234) pager: a log line")
	if _, text := p.tab.wait(t, chanPager, "in colour"); text != "I (31) pager: in colour" {
		t.Errorf("the pager channel shows %q; want the line without its colour codes", text)
	}
	if _, text := p.tab.wait(t, chanPager, "rst:0x1"); text != "rst:0x1 (POWERON)\tboot:0x8" {
		t.Errorf("the pager channel shows %q; want the line without its control characters, its tab kept", text)
	}

	if status, v := p.confirm(pagerFP, pageNets); status != http.StatusOK {
		t.Fatalf("POST /api/pair/confirm: %d %v; want 200", status, v)
	}
	p.tab.wait(t, chanPager, "Wi-Fi password", "hidden")
	p.tab.mu.Lock()
	for _, ev := range p.tab.events {
		if ev.channel == chanPager && strings.Contains(ev.text, "@P ") {
			t.Errorf("the pager channel shows a provisioning line: %.80q", ev.text)
		}
	}
	p.tab.mu.Unlock()
	p.noPassword()
}
