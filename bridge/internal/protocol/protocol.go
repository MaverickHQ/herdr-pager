// Package protocol is pager protocol v1 (parent design §Protocol): one JSON
// object per line over the SSH channel, device and bridge each sending their
// own set of messages.
//
// The contract is testdata/protocol/: a valid and an invalid example of every
// message, whose README states each rule that ParseDevice and ParseBridge
// apply. The firmware's codec reads the same files.
package protocol

// Limits. Line lengths don't count the '\n'; text lengths are bytes after
// JSON decoding.
const (
	Version       = 1    // the only protocol version
	MaxDeviceLine = 512  // a line from the device (parent 13.5)
	MaxBridgeLine = 8192 // a line from the bridge (parent 13.5)
	MaxAgents     = 16   // agents in one snapshot; `more` counts the rest
	MaxCardLines  = 4    // a card's detail lines: 4 since 2026-11-02, an MCP card's tool and 3 arguments (O-90), to a pager whose hello says so
	MinCardLines  = 3    // a card's detail lines to a pager whose hello has no lines
	MaxNameLen    = 24   // a device's name, an agent's display name
	MaxKindLen    = 10   // an agent kind
	MaxAtLen      = 16   // where an agent works: its folder's name
	MaxTextLen    = 40   // a card's summary, each of its lines
	MaxLabelLen   = 32   // a danger card's label: "DANGER: <label>" fills the 40-column row
	MaxHostLen    = 64   // the bridge's host name in its hello
	MaxPaneLen    = 32   // a pane id
	MaxSafeInt    = 1<<53 - 1
)

// A Message is one parsed line. Type is its "t".
type Message interface{ Type() string }

// Device → bridge: the complete allowlist (parent 13.1).

// DeviceHello opens a session. Its version is always Version. Lines is the
// most lines of a card the pager draws: its hello's optional lines, 3 or 4,
// or MinCardLines when it has none (a pager from before 2026-11-02; O-90's
// second review).
type DeviceHello struct {
	FW, Name string
	Lines    int
}

type Ping struct{ ID uint32 }

type List struct{ ID uint32 }

// Show asks for an agent's card. PG is the milliseconds since the agent's
// page fired, or -1 if it never paged.
type Show struct {
	ID   uint32
	Pane string
	PG   int64
}

// Approve and Deny are verdicts on the card with this seq and hash.
type Approve struct {
	ID   uint32
	Pane string
	Seq  uint64
	Hash string
}

type Deny struct {
	ID   uint32
	Pane string
	Seq  uint64
	Hash string
}

type Interrupt struct {
	ID   uint32
	Pane string
	Seq  uint64
}

type Jump struct {
	ID   uint32
	Pane string
}

func (*DeviceHello) Type() string { return "hello" }
func (*Ping) Type() string        { return "ping" }
func (*List) Type() string        { return "list" }
func (*Show) Type() string        { return "show" }
func (*Approve) Type() string     { return "approve" }
func (*Deny) Type() string        { return "deny" }
func (*Interrupt) Type() string   { return "interrupt" }
func (*Jump) Type() string        { return "jump" }

// Bridge → device.

// BridgeHello answers the device's hello. Its version is always Version.
type BridgeHello struct {
	Bridge, Host string
	Now          int64
}

// Snap is a snapshot: at most MaxAgents agents, with More counting the rest.
// Herdr is "ok" or "offline".
type Snap struct {
	Now    int64
	Herdr  string
	More   int
	Agents []Agent
}

// Agent is one snapshot entry. St is herdr's status; DSeq is its
// completion_seq, or 0; Since is when the bridge first saw Seq. At is where
// it works, its folder's name, or "" (sent only when set).
type Agent struct {
	Pane, Name, Kind, At, St string
	Seq, DSeq                uint64
	Since                    int64
	Can                      []string
}

// Card answers a show. Kind is "permission", "question" or "status"; Risk
// ("danger") and RL are set on a danger card only.
type Card struct {
	ID                   uint32
	Pane, St             string
	Seq                  uint64
	Hash, Kind, Risk, RL string
	Summary              string
	Lines, Can           []string
}

// Result answers a verdict, interrupt or jump. Err is set when OK is false.
type Result struct {
	ID  uint32
	OK  bool
	Err string
}

type Pong struct {
	ID  uint32
	Now int64
}

func (*BridgeHello) Type() string { return "hello" }
func (*Snap) Type() string        { return "snap" }
func (*Card) Type() string        { return "card" }
func (*Result) Type() string      { return "result" }
func (*Pong) Type() string        { return "pong" }

// An Error says why a line breaks the protocol. Field is "line" for its
// length, "json" for its syntax, "t" for its type, else the path of the
// offending key ("agents[0].can", "lines[0]"). It never quotes the line, so
// it is safe to log.
type Error struct{ Field, Reason string }

func (e *Error) Error() string { return "protocol: " + e.Field + ": " + e.Reason }
