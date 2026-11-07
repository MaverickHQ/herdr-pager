// Package provision pairs a pager over its USB serial console (parent design
// §Provisioning). Provisioning lines start with Prefix; every other line on
// the console is a log line.
//
// The contract is testdata/provisioning/: a valid and an invalid example of
// every line, whose README states each rule that ParseDevice and ParseTool
// apply, and the timeouts. The firmware's pairing code reads the same files.
package provision

const (
	Prefix   = "@P " // starts every provisioning line
	MaxLine  = 512   // bytes in a provisioning line, Prefix included, not its '\n'
	MaxWiFi  = 4     // networks in a prov_set (parent 16.1)
	MaxName  = 24    // a device's name
	MaxHost  = 64    // a prov_offer's host and host_name
	MaxSSID  = 32    // 802.11's limit
	KeyType  = "ssh-ed25519"
	keyBytes = 32 // an ed25519 public key
)

// A Message is one parsed line. Type is its "t".
type Message interface{ Type() string }

// Hello is prov_hello: the device's firmware version, its full public key
// ("ssh-ed25519 <base64>") and its pairing state: "waiting" for an offer,
// "confirming" one on its screen, or "confirmed" by BOOT and waiting for its
// prov_set.
type Hello struct{ FW, PubKey, State string }

// Result is prov_result. Err is set when OK is false: "cancelled",
// "timeout", "invalid", "busy" or "storage".
type Result struct {
	OK  bool
	Err string
}

// HostOffer is prov_offer, everything about the pairing that isn't secret
// (ADR 0011): the device's name, the address and port it dials, the
// bridge's full host key, and the Mac's name for the confirmation screen.
type HostOffer struct {
	Name     string
	Host     string
	Port     int
	HostKey  string
	HostName string
}

// Set is prov_set, sent only once the offer is confirmed on both sides: the
// confirmed offer's host key, and the networks in priority order.
type Set struct {
	HostKey string
	WiFi    []Network
}

// Network is one saved Wi-Fi network. Pass is empty for an open network.
type Network struct{ SSID, Pass string }

func (*Hello) Type() string     { return "prov_hello" }
func (*Result) Type() string    { return "prov_result" }
func (*HostOffer) Type() string { return "prov_offer" }
func (*Set) Type() string       { return "prov_set" }

// An Error says why a line breaks the contract. Field is "line" for its
// length, "prefix", "json" for its syntax, "t" for its type, else the path
// of the offending key ("wifi[0].pass"). It never quotes the line, so it
// never prints a password or the device's raw text.
type Error struct{ Field, Reason string }

func (e *Error) Error() string { return "provision: " + e.Field + ": " + e.Reason }
