package protocol

import (
	"encoding/json"
	"fmt"
)

// The bridge's messages on the wire, keys in the README's order.
type (
	wireHello struct {
		T      string `json:"t"`
		V      int    `json:"v"`
		Bridge string `json:"bridge"`
		Host   string `json:"host"`
		Now    int64  `json:"now"`
	}
	wireSnap struct {
		T      string      `json:"t"`
		Now    int64       `json:"now"`
		Herdr  string      `json:"herdr"`
		More   int         `json:"more"`
		Agents []wireAgent `json:"agents"`
	}
	wireAgent struct {
		Pane  string   `json:"pane"`
		Name  string   `json:"name"`
		Kind  string   `json:"kind"`
		At    string   `json:"at,omitempty"`
		St    string   `json:"st"`
		Seq   uint64   `json:"seq"`
		DSeq  uint64   `json:"dseq"`
		Since int64    `json:"since"`
		Can   []string `json:"can"`
	}
	wireCard struct {
		T       string   `json:"t"`
		ID      uint32   `json:"id"`
		Pane    string   `json:"pane"`
		St      string   `json:"st"`
		Seq     uint64   `json:"seq"`
		Hash    string   `json:"hash"`
		Kind    string   `json:"kind"`
		Risk    string   `json:"risk,omitempty"`
		RL      string   `json:"rl,omitempty"`
		Summary string   `json:"summary"`
		Lines   []string `json:"lines"`
		Can     []string `json:"can"`
	}
	wireResult struct {
		T   string `json:"t"`
		ID  uint32 `json:"id"`
		OK  bool   `json:"ok"`
		Err string `json:"err,omitempty"`
	}
	wirePong struct {
		T   string `json:"t"`
		ID  uint32 `json:"id"`
		Now int64  `json:"now"`
	}
)

// Encode writes one of the bridge's messages as its line, without the '\n'.
// It refuses a message the contract would reject (ParseBridge), so the
// bridge never sends a line the device must drop.
func Encode(m Message) ([]byte, error) {
	var w any
	switch m := m.(type) {
	case *BridgeHello:
		w = wireHello{"hello", Version, m.Bridge, m.Host, m.Now}
	case *Snap:
		agents := make([]wireAgent, len(m.Agents))
		for i, a := range m.Agents {
			agents[i] = wireAgent{a.Pane, a.Name, a.Kind, a.At, a.St, a.Seq, a.DSeq, a.Since, list(a.Can)}
		}
		w = wireSnap{"snap", m.Now, m.Herdr, m.More, agents}
	case *Card:
		w = wireCard{"card", m.ID, m.Pane, m.St, m.Seq, m.Hash, m.Kind, m.Risk, m.RL, m.Summary, list(m.Lines), list(m.Can)}
	case *Result:
		w = wireResult{"result", m.ID, m.OK, m.Err}
	case *Pong:
		w = wirePong{"pong", m.ID, m.Now}
	default:
		return nil, fmt.Errorf("protocol: the bridge doesn't send %T", m)
	}
	line, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	if _, err := ParseBridge(line); err != nil {
		return nil, err
	}
	return line, nil
}

// list is s, or an empty list: never null on the wire.
func list(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// IsPane says whether s is a pane id the protocol carries: herdr's full form,
// w1:p3 or w6:pE (herdr's ids are digits and letters), of at most MaxPaneLen bytes.
func IsPane(s string) bool { return len(s) <= MaxPaneLen && paneRE.MatchString(s) }
