// Package herdrtest is a fake herdr for tests (seam S2-1): a Unix socket that
// answers the five methods with herdr 0.9.3's shapes (testdata/herdr/socket/)
// and phase 0's captures, and records every method called. cmd/fakeherdr runs
// it as a program.
package herdrtest

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
)

// A Server is a running fake herdr.
type Server struct {
	Socket string // its socket path, in a short temp folder (os.MkdirTemp("", "hp"))

	l  net.Listener
	wg sync.WaitGroup

	mu        sync.Mutex
	agents    []herdr.AgentInfo
	tabs      []herdr.TabInfo       // tab.list's answer; none until SetTabs
	spaces    []herdr.WorkspaceInfo // workspace.list's
	detection map[string]string     // by pane id
	methods   []string
	sent      []Sent
	hang      bool      // read each request and never answer
	listReply []byte    // agent.list's reply line instead of the agents, if set
	log       io.Writer // where each request goes, if anywhere (LogRequests)
	conns     map[net.Conn]bool
	closed    bool
}

// A Sent is one agent.send_keys that herdr took: the pane and its keys.
type Sent struct {
	Pane string
	Keys []string
}

// Start serves a fake herdr on socket, with no agents.
func Start(socket string) (*Server, error) {
	l, err := net.Listen("unix", socket)
	if err != nil {
		return nil, err
	}
	s := &Server{Socket: socket, l: l, detection: map[string]string{}, conns: map[net.Conn]bool{}}
	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// SetAgents replaces the agents herdr has: what agent.list answers, and the
// agents agent.get finds by pane id.
func (s *Server) SetAgents(agents []herdr.AgentInfo) {
	for _, a := range agents {
		remember(all.names, a.Name, a.TitleStripped)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.agents = append([]herdr.AgentInfo(nil), agents...)
}

// SetTabs sets what tab.list and workspace.list answer: the numbers and
// labels herdr shows its tabs and workspaces by.
func (s *Server) SetTabs(tabs []herdr.TabInfo, spaces []herdr.WorkspaceInfo) {
	for _, t := range tabs {
		remember(all.names, &t.Label)
	}
	for _, w := range spaces {
		remember(all.names, &w.Label)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tabs = append([]herdr.TabInfo(nil), tabs...)
	s.spaces = append([]herdr.WorkspaceInfo(nil), spaces...)
}

// SetDetection sets the text agent.read answers for pane, as phase 0's
// detection captures hold it.
func (s *Server) SetDetection(pane, text string) {
	remember(all.panes, &text)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.detection[pane] = text
}

// LogRequests writes every request from now on to w, as one JSON line: its
// time, method and params (cmd/fakeherdr logs them on stdout).
func (s *Server) LogRequests(w io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.log = w
}

// Sent returns every agent.send_keys that herdr took so far, in order.
func (s *Server) Sent() []Sent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.sent)
}

// Hang makes the server read each request and never answer (true), as a
// stuck herdr would, or answer again (false).
func (s *Server) Hang(hang bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hang = hang
}

// SetListReply makes agent.list answer with line (without its '\n') instead
// of the agents: an error envelope, or a reply that won't decode. "" goes
// back to the agents.
func (s *Server) SetListReply(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listReply = nil
	if line != "" {
		s.listReply = []byte(line + "\n")
	}
}

// AllMethods returns every method called so far on every server started in
// this process, in order, so a suite can check them all once its tests have
// run (parent 13.2).
func AllMethods() []string {
	all.mu.Lock()
	defer all.mu.Unlock()
	return append([]string(nil), all.methods...)
}

var all = struct {
	mu      sync.Mutex
	methods []string
	names   map[string]bool
	panes   map[string]bool
}{names: map[string]bool{}, panes: map[string]bool{}}

// AllText returns every agent name and title (names), and every pane text
// (panes), given to any server started in this process through SetAgents and
// SetDetection, each once, so a suite can check its logs for them once its
// tests have run (parent 11.5).
func AllText() (names, panes []string) {
	all.mu.Lock()
	defer all.mu.Unlock()
	return slices.Sorted(maps.Keys(all.names)), slices.Sorted(maps.Keys(all.panes))
}

// remember adds each of texts that isn't nil to set, one of all's.
func remember(set map[string]bool, texts ...*string) {
	all.mu.Lock()
	defer all.mu.Unlock()
	for _, t := range texts {
		if t != nil {
			set[*t] = true
		}
	}
}

// Methods returns every method called so far, in order.
func (s *Server) Methods() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.methods...)
}

// Close stops the server, removing its socket, closes its connections and
// waits for them to end.
func (s *Server) Close() error {
	err := s.l.Close()
	s.mu.Lock()
	s.closed = true
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
	return err
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.l.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			conn.Close()
			return
		}
		s.conns[conn] = true
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			s.answer(conn)
			conn.Close()
			s.mu.Lock()
			delete(s.conns, conn)
			s.mu.Unlock()
		}()
	}
}

// answer reads one request line and writes one reply line, as herdr does.
func (s *Server) answer(conn net.Conn) {
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req herdr.Request
	if err := json.Unmarshal(line, &req); err != nil {
		write(conn, "", nil, &herdr.Error{Code: "invalid_request", Message: "invalid request: " + err.Error()})
		return
	}
	all.mu.Lock()
	all.methods = append(all.methods, req.Method)
	all.mu.Unlock()
	s.mu.Lock()
	s.methods = append(s.methods, req.Method)
	agents := append([]herdr.AgentInfo(nil), s.agents...)
	tabs, spaces := append([]herdr.TabInfo{}, s.tabs...), append([]herdr.WorkspaceInfo{}, s.spaces...)
	hang, listReply := s.hang, s.listReply
	if s.log != nil {
		entry, _ := json.Marshal(struct {
			Time   time.Time       `json:"time"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params,omitempty"`
		}{time.Now(), req.Method, req.Params})
		s.log.Write(append(entry, '\n'))
	}
	s.mu.Unlock()
	if hang {
		conn.Read(make([]byte, 1)) // until the client gives up, or Close
		return
	}
	if req.Method == herdr.MethodList && listReply != nil {
		conn.Write(listReply)
		return
	}

	var target herdr.Target
	json.Unmarshal(req.Params, &target)
	agent, found := find(agents, target.Target)
	notFound := &herdr.Error{Code: "agent_not_found", Message: fmt.Sprintf("agent target %s not found", target.Target)}

	switch req.Method {
	case herdr.MethodList:
		wire := make([]json.RawMessage, len(agents))
		for i, a := range agents {
			wire[i] = Wire(a)
		}
		write(conn, req.ID, map[string]any{"type": "agent_list", "agents": wire}, nil)
	case herdr.MethodTabs:
		write(conn, req.ID, map[string]any{"type": "tab_list", "tabs": tabs}, nil)
	case herdr.MethodWorkspaces:
		write(conn, req.ID, map[string]any{"type": "workspace_list", "workspaces": spaces}, nil)
	case herdr.MethodGet:
		if !found {
			write(conn, req.ID, nil, notFound)
			return
		}
		write(conn, req.ID, map[string]any{"type": "agent_info", "agent": Wire(agent)}, nil)
	case herdr.MethodRead:
		if !found {
			write(conn, req.ID, nil, notFound)
			return
		}
		s.mu.Lock()
		text := s.detection[agent.PaneID]
		s.mu.Unlock()
		write(conn, req.ID, map[string]any{"type": "pane_read", "read": herdr.PaneRead{
			PaneID: agent.PaneID, WorkspaceID: agent.WorkspaceID, TabID: agent.TabID,
			Source: "detection", Format: "text", Text: text,
		}}, nil)
	case herdr.MethodSendKeys:
		// herdr queues the keys to the pane; the agent changes only when the
		// test, or fakeherdr's scenario, says so.
		var p herdr.SendKeysParams
		json.Unmarshal(req.Params, &p)
		if !found {
			write(conn, req.ID, nil, notFound)
			return
		}
		s.mu.Lock()
		s.sent = append(s.sent, Sent{Pane: agent.PaneID, Keys: p.Keys})
		s.mu.Unlock()
		write(conn, req.ID, map[string]any{"type": "ok"}, nil)
	case herdr.MethodFocus:
		if !found {
			write(conn, req.ID, nil, notFound)
			return
		}
		s.mu.Lock()
		for i := range s.agents {
			s.agents[i].Focused = s.agents[i].PaneID == agent.PaneID
		}
		s.mu.Unlock()
		agent.Focused = true
		write(conn, req.ID, map[string]any{"type": "agent_info", "agent": Wire(agent)}, nil)
	default:
		write(conn, req.ID, nil, &herdr.Error{Code: "invalid_request", Message: fmt.Sprintf("invalid request: unknown variant `%s`", req.Method)})
	}
}

func find(agents []herdr.AgentInfo, pane string) (herdr.AgentInfo, bool) {
	for _, a := range agents {
		if a.PaneID == pane {
			return a, true
		}
	}
	return herdr.AgentInfo{}, false
}

func write(conn net.Conn, id string, result any, herr *herdr.Error) {
	reply := map[string]any{"id": id}
	if herr != nil {
		reply["error"] = herr
	} else {
		reply["result"] = result
	}
	line, _ := json.Marshal(reply)
	conn.Write(append(line, '\n'))
}

// Wire is a as herdr writes it: optional fields omitted, never null.
func Wire(a herdr.AgentInfo) json.RawMessage {
	data, _ := json.Marshal(a)
	var fields map[string]json.RawMessage
	json.Unmarshal(data, &fields)
	for k, v := range fields {
		if string(v) == "null" {
			delete(fields, k)
		}
	}
	data, _ = json.Marshal(fields)
	return data
}

// Agent reads the agent of an agent.get reply saved in a file: one of
// herdr's wire lines (testdata/herdr/socket/agent.get/) or one of phase 0's
// CLI captures (testdata/herdr/claude/get/).
func Agent(path string) (herdr.AgentInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return herdr.AgentInfo{}, err
	}
	var r struct {
		Result struct {
			Type  string           `json:"type"`
			Agent *herdr.AgentInfo `json:"agent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return herdr.AgentInfo{}, fmt.Errorf("%s: %w", path, err)
	}
	if r.Result.Type != "agent_info" || r.Result.Agent == nil {
		return herdr.AgentInfo{}, errors.New(path + ": not an agent_info reply")
	}
	return *r.Result.Agent, nil
}

// Agents reads the agents of an agent.list reply saved in a file: one of
// herdr's wire lines (testdata/herdr/socket/agent.list/) or one of phase 0's
// CLI captures (testdata/herdr/claude/list/).
func Agents(path string) ([]herdr.AgentInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r struct {
		Result struct {
			Type   string            `json:"type"`
			Agents []herdr.AgentInfo `json:"agents"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if r.Result.Type != "agent_list" {
		return nil, errors.New(path + ": not an agent_list reply")
	}
	return r.Result.Agents, nil
}
