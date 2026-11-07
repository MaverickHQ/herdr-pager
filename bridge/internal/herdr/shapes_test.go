package herdr

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// socketDir holds herdr 0.9.3's socket shapes, from its own API schema
// (design D2-11; its README says where each line comes from).
const socketDir = "../../testdata/herdr/socket"

// TestSocketFixturesDecode checks the client's types against every request,
// ok and error file: the bridge's requests are those lines byte for byte;
// each reply unwraps to its method's result (agent_list, agent_info for get
// and focus, pane_read, ok); each error envelope yields its code. Every file
// in the folder is checked.
func TestSocketFixturesDecode(t *testing.T) {
	seen := map[string]bool{}
	line := func(name string) []byte {
		t.Helper()
		seen[name] = true
		data, err := os.ReadFile(filepath.Join(socketDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.HasSuffix(data, []byte("\n")) || bytes.Count(data, []byte("\n")) != 1 {
			t.Fatalf("%s: not exactly one line ending in a newline", name)
		}
		return data[:len(data)-1]
	}

	t.Run("requests", func(t *testing.T) {
		for _, c := range []struct {
			file   string
			method string
			params any
		}{
			{"agent.list/request.json", MethodList, Empty{}},
			{"agent.get/request.json", MethodGet, Target{Target: "w1:p1"}},
			{"agent.read/request.json", MethodRead, ReadParams{Target: "w1:p1", Source: "detection", Format: "text"}},
			{"agent.send_keys/request.json", MethodSendKeys, SendKeysParams{Target: "w1:p1", Keys: []string{"1"}}},
			{"agent.focus/request.json", MethodFocus, Target{Target: "w1:p1"}},
			{"tab.list/request.json", MethodTabs, Empty{}},
			{"workspace.list/request.json", MethodWorkspaces, Empty{}},
		} {
			want := line(c.file)
			got, err := newRequest(c.method, c.params)
			if err != nil {
				t.Errorf("%s: %v", c.file, err)
			} else if !bytes.Equal(got, append(want, '\n')) {
				t.Errorf("%s: the bridge would send\n%s\nwant\n%s", c.file, got, want)
			}
			var r Request
			dec := json.NewDecoder(bytes.NewReader(want))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&r); err != nil {
				t.Errorf("%s: doesn't decode as a Request: %v", c.file, err)
				continue
			}
			p := reflect.New(reflect.TypeOf(c.params))
			pdec := json.NewDecoder(bytes.NewReader(r.Params))
			pdec.DisallowUnknownFields()
			if err := pdec.Decode(p.Interface()); err != nil {
				t.Errorf("%s: params don't decode as %T: %v", c.file, c.params, err)
			} else if !reflect.DeepEqual(p.Elem().Interface(), c.params) {
				t.Errorf("%s: params decode as %+v, want %+v", c.file, p.Elem().Interface(), c.params)
			}
		}
	})

	t.Run("deliberately invalid requests", func(t *testing.T) {
		if json.Valid(line("envelope/request-bad-json.txt")) {
			t.Error("request-bad-json.txt is valid JSON; it's meant to be cut short")
		}
		for _, file := range []string{"envelope/request-unknown-method.json", "envelope/request-missing-keys.json"} {
			var r Request
			if err := json.Unmarshal(line(file), &r); err != nil {
				t.Errorf("%s: doesn't decode as a Request: %v", file, err)
				continue
			}
			var p SendKeysParams
			_ = json.Unmarshal(r.Params, &p)
			if r.Method != "agent.send_keys" {
				p.Keys = []string{"1"} // so that only the method can refuse it
			}
			if _, err := newRequest(r.Method, p); err == nil {
				t.Errorf("%s: the bridge would send it", file)
			}
		}
	})

	str := func(s string) *string { return &s }
	num := func(n uint64) *uint64 { return &n }
	recon := AgentInfo{
		TerminalID: "term_65e59fc19e7cb1", PaneID: "w1:p1", WorkspaceID: "w1", TabID: "w1:t1",
		Agent: str("claude"), Name: str("recon"), TitleStripped: str("Touch approved.txt command"),
		Status: "blocked", StateChangeSeq: 3, Revision: 2,
		Cwd: "/private/tmp/herdr-pager-recon", ForegroundCwd: "/private/tmp/herdr-pager-recon",
	}

	t.Run("agent.list", func(t *testing.T) {
		agents, err := decodeList(line("agent.list/ok.json"))
		if err != nil || !reflect.DeepEqual(agents, []AgentInfo{recon}) {
			t.Errorf("ok.json: %+v, %v; want [%+v]", agents, err, recon)
		}
		agents, err = decodeList(line("agent.list/ok-empty.json"))
		if err != nil || agents == nil || len(agents) != 0 {
			t.Errorf("ok-empty.json: %+v, %v; want an empty list", agents, err)
		}
		agents, err = decodeList(line("agent.list/ok-unmanaged-inferred.json"))
		typed := AgentInfo{
			TerminalID: "term_0000000000test", PaneID: "w2:p4", WorkspaceID: "w2", TabID: "w2:t3",
			Agent: str("claude"), TitleStripped: str("Touch approved.txt command"),
			Status: "working", StateChangeSeq: 14, Revision: 2,
			Cwd: "/private/tmp/example", ForegroundCwd: "/private/tmp/example",
		}
		if err != nil || !reflect.DeepEqual(agents, []AgentInfo{recon, typed}) {
			t.Errorf("ok-unmanaged-inferred.json: %+v, %v; want [%+v %+v] (no name)", agents, err, recon, typed)
		}
	})

	t.Run("agent.get and agent.focus unwrap agent_info", func(t *testing.T) {
		info, err := decodeAgent(line("agent.get/ok.json"))
		if err != nil || !reflect.DeepEqual(info, recon) {
			t.Errorf("agent.get/ok.json: %+v, %v; want %+v", info, err, recon)
		}
		focused := recon
		focused.Status, focused.Focused, focused.StateChangeSeq, focused.CompletionSeq = "idle", true, 29, num(29)
		info, err = decodeAgent(line("agent.focus/ok.json"))
		if err != nil || !reflect.DeepEqual(info, focused) {
			t.Errorf("agent.focus/ok.json: %+v, %v; want %+v", info, err, focused)
		}
	})

	t.Run("agent.read unwraps pane_read", func(t *testing.T) {
		capture, err := os.ReadFile("../../testdata/herdr/claude/detection/shell-0s.txt")
		if err != nil {
			t.Fatal(err)
		}
		read, err := decodeRead(line("agent.read/ok.json"))
		want := PaneRead{PaneID: "w1:p1", WorkspaceID: "w1", TabID: "w1:t1", Source: "detection", Format: "text", Text: string(capture)}
		if err != nil || !reflect.DeepEqual(read, want) {
			t.Errorf("agent.read/ok.json: %+v, %v; want the shell-0s capture", read, err)
		}
	})

	t.Run("agent.send_keys", func(t *testing.T) {
		if err := decodeOK(line("agent.send_keys/ok.json")); err != nil {
			t.Errorf("ok.json: %v", err)
		}
	})

	t.Run("error envelopes yield their code", func(t *testing.T) {
		decoders := map[string]func([]byte) error{
			"list":      func(b []byte) error { _, err := decodeList(b); return err },
			"agent":     func(b []byte) error { _, err := decodeAgent(b); return err },
			"read":      func(b []byte) error { _, err := decodeRead(b); return err },
			"send_keys": decodeOK,
		}
		for _, c := range []struct{ file, decoder, code string }{
			{"agent.list/error-server-unavailable.json", "list", "server_unavailable"},
			{"agent.get/error-agent-not-found.json", "agent", "agent_not_found"},
			{"agent.focus/error-agent-not-found.json", "agent", "agent_not_found"},
			{"agent.read/error-agent-not-found.json", "read", "agent_not_found"},
			{"agent.send_keys/error-agent-not-found.json", "send_keys", "agent_not_found"},
			{"agent.send_keys/error-agent-not-ready.json", "send_keys", "agent_not_ready"},
			{"agent.send_keys/error-invalid-key.json", "send_keys", "invalid_key"},
			{"envelope/error-invalid-request-bad-json.json", "list", "invalid_request"},
			{"envelope/error-invalid-request-missing-field.json", "send_keys", "invalid_request"},
			{"envelope/error-invalid-request-unknown-method.json", "agent", "invalid_request"},
		} {
			err := decoders[c.decoder](line(c.file))
			var herr *Error
			if !errors.As(err, &herr) || herr.Code != c.code {
				t.Errorf("%s: %v; want herdr error %q", c.file, err, c.code)
			}
		}
	})

	t.Run("tab.list and workspace.list: the numbers and labels herdr shows", func(t *testing.T) {
		tabs, err := decodeTabs(line("tab.list/ok.json"))
		if want := []TabInfo{{TabID: "w1:t1", WorkspaceID: "w1", Number: 1, Label: "recon"}}; err != nil || !reflect.DeepEqual(tabs, want) {
			t.Errorf("tab.list/ok.json: %+v, %v; want %+v", tabs, err, want)
		}
		spaces, err := decodeWorkspaces(line("workspace.list/ok.json"))
		if want := []WorkspaceInfo{{WorkspaceID: "w1", Number: 1, Label: "herdr-pager"}}; err != nil || !reflect.DeepEqual(spaces, want) {
			t.Errorf("workspace.list/ok.json: %+v, %v; want %+v", spaces, err, want)
		}
		if _, err := decodeTabs(line("agent.list/ok.json")); !errors.Is(err, ErrBadReply) {
			t.Errorf("agent_list read as tab_list: %v; want ErrBadReply", err)
		}
	})

	t.Run("a status outside herdr's five doesn't decode", func(t *testing.T) {
		// herdr's schema has five statuses (idle, working, blocked, done,
		// unknown), and phase 2's R5.4 treats a reply that doesn't decode as a
		// herdr error. Group 13's review: one agent's "waiting" broke every
		// snapshot without a word.
		for _, status := range []string{"waiting", ""} {
			swap := func(name string) []byte {
				return bytes.Replace(line(name), []byte(`"agent_status":"blocked"`), []byte(`"agent_status":"`+status+`"`), 1)
			}
			if _, err := decodeList(swap("agent.list/ok.json")); !errors.Is(err, ErrBadReply) {
				t.Errorf("agent.list with status %q: %v; want ErrBadReply", status, err)
			}
			if _, err := decodeAgent(swap("agent.get/ok.json")); !errors.Is(err, ErrBadReply) {
				t.Errorf("agent.get with status %q: %v; want ErrBadReply", status, err)
			}
		}
	})

	t.Run("a reply of the wrong type doesn't decode", func(t *testing.T) {
		if _, err := decodeAgent(line("agent.list/ok.json")); !errors.Is(err, ErrBadReply) {
			t.Errorf("agent_list read as agent_info: %v; want ErrBadReply", err)
		}
		if _, err := decodeList(line("agent.send_keys/ok.json")); !errors.Is(err, ErrBadReply) {
			t.Errorf("ok read as agent_list: %v; want ErrBadReply", err)
		}
	})

	err := filepath.WalkDir(socketDir, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() || e.Name() == "README.md" {
			return err
		}
		rel, _ := filepath.Rel(socketDir, path)
		if !seen[filepath.ToSlash(rel)] {
			t.Errorf("%s isn't checked", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
