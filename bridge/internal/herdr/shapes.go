// Package herdr is the bridge's only way to herdr: one JSON request per
// connection on herdr's Unix socket, and only the five methods the bridge may
// call (parent 13.2).
//
// The shapes are herdr 0.9.3's (protocol 22), from its own API schema
// (design D2-11): testdata/herdr/socket/ holds one line of each, and the
// tests check these types against every file there.
package herdr

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
)

// The methods the bridge may call (parent 13.2): the five agent calls, and
// since the desk checkpoint (2026-10-24, the owner's decision) two read-only
// lists for the labels herdr shows its tabs and workspaces by.
const (
	MethodList       = "agent.list"
	MethodGet        = "agent.get"
	MethodRead       = "agent.read"
	MethodSendKeys   = "agent.send_keys"
	MethodFocus      = "agent.focus"
	MethodTabs       = "tab.list"
	MethodWorkspaces = "workspace.list"
)

var methods = []string{MethodList, MethodGet, MethodRead, MethodSendKeys, MethodFocus, MethodTabs, MethodWorkspaces}

// TabInfo is a tab as tab.list sends it, cut to what the bridge reads:
// Number is the tab's number within its workspace and Label its name, both
// as herdr's tab bar shows them.
type TabInfo struct {
	TabID       string `json:"tab_id"`
	WorkspaceID string `json:"workspace_id"`
	Number      uint   `json:"number"`
	Label       string `json:"label"`
}

// WorkspaceInfo is a workspace as workspace.list sends it, cut the same way.
// Number is what herdr's sidebar shows, not the id's counter (w6 can be 5).
type WorkspaceInfo struct {
	WorkspaceID string `json:"workspace_id"`
	Number      uint   `json:"number"`
	Label       string `json:"label"`
}

// AgentInfo is one agent as herdr 0.9.3 sends it. Optional fields are
// omitted, never null. The bridge keys an agent by TerminalID and addresses
// it by its current PaneID.
type AgentInfo struct {
	TerminalID     string  `json:"terminal_id"`
	PaneID         string  `json:"pane_id"` // "w1:p3": the target of every call; changes if the pane moves
	WorkspaceID    string  `json:"workspace_id"`
	TabID          string  `json:"tab_id"`
	Agent          *string `json:"agent"`                   // the kind, e.g. "claude": the keymap's key
	Name           *string `json:"name"`                    // only for agents started with agent.start or renamed
	TitleStripped  *string `json:"terminal_title_stripped"` // the title
	Status         string  `json:"agent_status"`            // idle, working, blocked, done or unknown
	StateChangeSeq uint64  `json:"state_change_seq"`        // one counter shared by all agents
	CompletionSeq  *uint64 `json:"completion_seq"`          // which completion; not a counter
	Focused        bool    `json:"focused"`
	Cwd            string  `json:"cwd"`            // the pane's working directory
	ForegroundCwd  string  `json:"foreground_cwd"` // its foreground process's (the agent's)
	Revision       uint64  `json:"revision"`
}

// PaneRead is agent.read's result. For the bridge's reads, Source is
// "detection" and Format "text".
type PaneRead struct {
	PaneID      string `json:"pane_id"`
	WorkspaceID string `json:"workspace_id"`
	TabID       string `json:"tab_id"`
	Source      string `json:"source"`
	Format      string `json:"format"`
	Text        string `json:"text"`
	Revision    uint64 `json:"revision"`
	Truncated   bool   `json:"truncated"`
}

// Request is one request line. Params is always sent, {} when empty.
type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// The params of each method: Empty for agent.list, Target for agent.get and
// agent.focus.
type (
	Empty  struct{}
	Target struct {
		Target string `json:"target"`
	}
	ReadParams struct {
		Target string `json:"target"`
		Source string `json:"source"`
		Format string `json:"format"`
		Lines  *int   `json:"lines,omitempty"`
	}
	SendKeysParams struct {
		Target string   `json:"target"`
		Keys   []string `json:"keys"`
	}
)

// An Error is herdr's error envelope. Match Code, never Message.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error names the code only: herdr's message can quote a pane's target.
func (e *Error) Error() string { return "herdr: " + e.safeCode() }

// codeShape is the shape of herdr's error codes, such as agent_not_found:
// lower case and underscores, at most 32 bytes. A shape, not a list, so a code
// a later herdr adds still shows (the owner's decision, 2026-10-27,
// open-items O-19).
var codeShape = regexp.MustCompile(`^[a-z_]{1,32}$`)

// safeCode is e's code if it has codeShape, else `other`: a changed herdr
// can't put a target or an agent's text in the log through it.
func (e *Error) safeCode() string {
	if codeShape.MatchString(e.Code) {
		return e.Code
	}
	return "other"
}

// ErrBadReply is a reply that doesn't decode, or whose result isn't the type
// its method expects.
var ErrBadReply = errors.New("herdr: reply doesn't decode")

// newRequest builds the line, '\n' included, that asks method with params.
// It refuses a method outside the five, and a send_keys without keys.
func newRequest(method string, params any) ([]byte, error) {
	if !slices.Contains(methods, method) {
		return nil, fmt.Errorf("herdr: the bridge never calls %q", method)
	}
	if p, ok := params.(SendKeysParams); ok && len(p.Keys) == 0 {
		return nil, errors.New("herdr: send_keys without keys")
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	line, err := json.Marshal(Request{ID: "pager-bridge:" + method, Method: method, Params: raw})
	if err != nil {
		return nil, err
	}
	return append(line, '\n'), nil
}

// decodeReply reads one reply line: the error envelope first, then a result
// of type want, decoded into result.
func decodeReply(line []byte, want string, result any) error {
	var r struct {
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
	}
	if err := json.Unmarshal(line, &r); err != nil {
		return fmt.Errorf("%w: %v", ErrBadReply, err)
	}
	if r.Error != nil {
		if r.Error.Code == "" {
			return fmt.Errorf("%w: an error without a code", ErrBadReply)
		}
		return r.Error
	}
	var head struct {
		Type string `json:"type"`
	}
	if r.Result == nil || json.Unmarshal(r.Result, &head) != nil || head.Type != want {
		return fmt.Errorf("%w: the result isn't %s", ErrBadReply, want)
	}
	if err := json.Unmarshal(r.Result, result); err != nil {
		return fmt.Errorf("%w: %v", ErrBadReply, err)
	}
	return nil
}

// decodeList unwraps agent.list's {type:"agent_list", agents}.
func decodeList(line []byte) ([]AgentInfo, error) {
	var r struct {
		Agents []AgentInfo `json:"agents"`
	}
	if err := decodeReply(line, "agent_list", &r); err != nil {
		return nil, err
	}
	if r.Agents == nil {
		return nil, fmt.Errorf("%w: agent_list without agents", ErrBadReply)
	}
	for _, a := range r.Agents {
		if err := checkStatus(a); err != nil {
			return nil, err
		}
	}
	return r.Agents, nil
}

// statuses are the agent statuses herdr's schema has (parent 4.1).
var statuses = map[string]bool{"idle": true, "working": true, "blocked": true, "done": true, "unknown": true}

// checkStatus refuses an agent whose status isn't one of herdr's five, so
// that the reply holding it doesn't decode (phase 2's R5.4): a status the
// protocol can't carry would otherwise break every snapshot. The error never
// quotes the status.
func checkStatus(a AgentInfo) error {
	if !statuses[a.Status] {
		return fmt.Errorf("%w: an agent status outside herdr's five", ErrBadReply)
	}
	return nil
}

// decodeTabs unwraps tab.list's {type:"tab_list", tabs}.
func decodeTabs(line []byte) ([]TabInfo, error) {
	var r struct {
		Tabs []TabInfo `json:"tabs"`
	}
	if err := decodeReply(line, "tab_list", &r); err != nil {
		return nil, err
	}
	if r.Tabs == nil {
		return nil, fmt.Errorf("%w: tab_list without tabs", ErrBadReply)
	}
	return r.Tabs, nil
}

// decodeWorkspaces unwraps workspace.list's {type:"workspace_list", workspaces}.
func decodeWorkspaces(line []byte) ([]WorkspaceInfo, error) {
	var r struct {
		Workspaces []WorkspaceInfo `json:"workspaces"`
	}
	if err := decodeReply(line, "workspace_list", &r); err != nil {
		return nil, err
	}
	if r.Workspaces == nil {
		return nil, fmt.Errorf("%w: workspace_list without workspaces", ErrBadReply)
	}
	return r.Workspaces, nil
}

// decodeAgent unwraps agent.get's and agent.focus's {type:"agent_info", agent}.
func decodeAgent(line []byte) (AgentInfo, error) {
	var r struct {
		Agent *AgentInfo `json:"agent"`
	}
	if err := decodeReply(line, "agent_info", &r); err != nil {
		return AgentInfo{}, err
	}
	if r.Agent == nil {
		return AgentInfo{}, fmt.Errorf("%w: agent_info without agent", ErrBadReply)
	}
	if err := checkStatus(*r.Agent); err != nil {
		return AgentInfo{}, err
	}
	return *r.Agent, nil
}

// decodeRead unwraps agent.read's {type:"pane_read", read}.
func decodeRead(line []byte) (PaneRead, error) {
	var r struct {
		Read *PaneRead `json:"read"`
	}
	if err := decodeReply(line, "pane_read", &r); err != nil {
		return PaneRead{}, err
	}
	if r.Read == nil {
		return PaneRead{}, fmt.Errorf("%w: pane_read without read", ErrBadReply)
	}
	return *r.Read, nil
}

// decodeOK reads agent.send_keys's {type:"ok"}: the keys were queued to the
// pane, not necessarily acted on.
func decodeOK(line []byte) error {
	var r struct{}
	return decodeReply(line, "ok", &r)
}
