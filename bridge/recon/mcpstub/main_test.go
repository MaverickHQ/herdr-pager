package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStubAnswersTheHandshakeAndListsEcho: the stub answers initialize with
// the client's protocol version and its own name, ignores notifications,
// lists one tool, echo, taking a text argument, and answers ping and an
// unknown method with an error, one JSON line per request with an id.
func TestStubAnswersTheHandshakeAndListsEcho(t *testing.T) {
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":"three","method":"ping"}`,
		`{"jsonrpc":"2.0","id":4,"method":"resources/list"}`,
		`not json`,
	}, "\n") + "\n"
	var out bytes.Buffer
	if err := serve(strings.NewReader(in), &out, ""); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("%d replies, want 5 (no reply to a notification): %q", len(lines), lines)
	}
	var init struct {
		ID     int `json:"id"`
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
			Capabilities map[string]any `json:"capabilities"`
		} `json:"result"`
	}
	mustDecode(t, lines[0], &init)
	if init.ID != 1 || init.Result.ProtocolVersion != "2025-06-18" || init.Result.ServerInfo.Name != "recon" || init.Result.Capabilities["tools"] == nil {
		t.Errorf("initialize: %s", lines[0])
	}
	var list struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				InputSchema struct {
					Properties map[string]any `json:"properties"`
					Required   []string       `json:"required"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	mustDecode(t, lines[1], &list)
	if tl := list.Result.Tools; len(tl) != 2 || tl[0].Name != "echo" || tl[0].InputSchema.Properties["text"] == nil || len(tl[0].InputSchema.Required) != 1 ||
		tl[1].Name != "note" || !strings.Contains(tl[1].Description, "\n\n") || tl[1].InputSchema.Properties["text"] == nil {
		t.Errorf("tools/list: %s", lines[1])
	}
	if !strings.Contains(lines[2], `"id":"three"`) || !strings.Contains(lines[2], `"result":{}`) {
		t.Errorf("ping: %s", lines[2])
	}
	if !strings.Contains(lines[3], `"id":4`) || !strings.Contains(lines[3], `"code":-32601`) {
		t.Errorf("an unknown method: %s", lines[3])
	}
	if !strings.Contains(lines[4], `"code":-32700`) {
		t.Errorf("a line that isn't JSON: %s", lines[4])
	}
}

// TestStubEchoesAndLogsEachCall: tools/call on echo returns its text, and
// appends one line to the log, so the recon can tell whether a denied call
// ran anyway. Another tool is an error, and logs nothing.
func TestStubEchoesAndLogsEachCall(t *testing.T) {
	log := filepath.Join(t.TempDir(), "calls.log")
	in := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"text":"hi"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"rm","arguments":{}}}` + "\n"
	var out bytes.Buffer
	if err := serve(strings.NewReader(in), &out, log); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d replies, want 2: %q", len(lines), lines)
	}
	var call struct {
		Result struct {
			Content []struct {
				Type, Text string
			} `json:"content"`
		} `json:"result"`
	}
	mustDecode(t, lines[0], &call)
	if c := call.Result.Content; len(c) != 1 || c[0].Type != "text" || c[0].Text != "hi" {
		t.Errorf("tools/call echo: %s", lines[0])
	}
	if !strings.Contains(lines[1], `"isError":true`) {
		t.Errorf("tools/call on another tool: %s", lines[1])
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "\n"); got != 1 {
		t.Errorf("the log holds %d lines, want 1: %q", got, data)
	}
}

func mustDecode(t *testing.T, line string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(line), v); err != nil {
		t.Fatalf("%v: %s", err, line)
	}
}
