// Command mcpstub is a stub MCP server for the recon (bridge/recon,
// capture-claude.sh; open items O-88 and O-90): a nested Claude Code calls
// its tools, so the recon can capture Claude Code's permission prompt for an
// MCP tool call, and deny it.
//
// It speaks MCP over stdio only, newline-delimited JSON-RPC 2.0, with the
// standard library alone: initialize, tools/list (two tools, echo and note,
// each taking a text argument; note's description runs over several lines),
// tools/call (echo returns its text, note "noted") and ping. It opens no
// socket, runs nothing, and reads and writes no file but the optional log.
//
// Usage:
//
//	mcpstub [--log <file>]
//
// With --log, each tools/call appends one line to the file ("call echo"), so
// the recon can check that a denied call never ran.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	logPath := flag.String("log", "", "append a line to this file for each tools/call")
	flag.Parse()
	if err := serve(os.Stdin, os.Stdout, *logPath); err != nil {
		fmt.Fprintln(os.Stderr, "mcpstub:", err)
		os.Exit(1)
	}
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// The tools: echo, and note, whose description runs over several lines,
// with blank lines and markdown, to see how Claude Code draws one (O-90's
// review).
var echoTool = map[string]any{
	"name":        "echo",
	"description": "Returns the text it is given.",
	"inputSchema": map[string]any{
		"type":       "object",
		"properties": map[string]any{"text": map[string]any{"type": "string", "description": "The text to return."}},
		"required":   []string{"text"},
	},
}

var noteTool = map[string]any{
	"name":        "note",
	"description": "Keeps a note.\n\nUse it for **short** notes:\n\n- one line each\n- nothing secret\n\nSee `NOTES.md`.",
	"inputSchema": map[string]any{
		"type":       "object",
		"properties": map[string]any{"text": map[string]any{"type": "string", "description": "The note."}},
		"required":   []string{"text"},
	},
}

// serve answers each request line on in with one line on out until in ends.
// A notification (no id) gets no answer.
func serve(in io.Reader, out io.Writer, logPath string) error {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	enc := json.NewEncoder(out)
	for sc.Scan() {
		var req request
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			if err := enc.Encode(response{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue // a notification
		}
		resp := response{JSONRPC: "2.0", ID: req.ID}
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.ProtocolVersion == "" {
				p.ProtocolVersion = "2025-06-18"
			}
			resp.Result = map[string]any{
				"protocolVersion": p.ProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "recon", "version": "1"},
			}
		case "ping":
			resp.Result = struct{}{}
		case "tools/list":
			resp.Result = map[string]any{"tools": []any{echoTool, noteTool}}
		case "tools/call":
			resp.Result = call(req.Params, logPath)
		default:
			resp.Error = &rpcError{-32601, "method not found"}
		}
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return sc.Err()
}

// call runs tools/call: echo returns its text, and note "noted", each
// logged first; any other tool is an error.
func call(params json.RawMessage, logPath string) any {
	var p struct {
		Name      string `json:"name"`
		Arguments struct {
			Text string `json:"text"`
		} `json:"arguments"`
	}
	_ = json.Unmarshal(params, &p)
	text := func(s string) []any { return []any{map[string]any{"type": "text", "text": s}} }
	if p.Name != "echo" && p.Name != "note" {
		return map[string]any{"content": text("no such tool"), "isError": true}
	}
	if logPath != "" {
		f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err == nil {
			fmt.Fprintln(f, "call", p.Name)
			f.Close()
		}
	}
	if p.Name == "note" {
		return map[string]any{"content": text("noted")}
	}
	return map[string]any{"content": text(p.Arguments.Text)}
}
