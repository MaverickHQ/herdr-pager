package provision

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
)

// Options are provision's flags, with the passwords read from stdin, and
// what it needs from the bridge.
type Options struct {
	Port     string    // --port: the serial device
	Name     string    // --name
	Networks []Network // --wifi-ssid, each with its password from stdin, in order
	Host     string    // --host; "" for <LocalHostName>.local (phase 2 R4.3)

	BridgePort int           // the port the bridge listens on (config.toml's listen)
	HostKey    ssh.PublicKey // the bridge's host key
	Devices    string        // devices.toml's path
	Out        io.Writer     // what the owner reads: progress and both fingerprints, never a password

	// LocalHostName is the Mac's name, for host_name (and host, when Host is "").
	LocalHostName func() (string, error)
	// KnownNetworks are the Wi-Fi networks the Mac has saved, for the
	// near-miss warning (KnownNetworks); nil for none.
	KnownNetworks func() []string
	// After and Now are the clock; nil for the real one.
	After func(time.Duration) <-chan time.Time
	Now   func() time.Time

	// Confirm is Run's second confirmation (ADR 0011): once the pager has
	// confirmed the offer with BOOT, it asks the owner whether both
	// fingerprints Run printed match the pager's screen, and returns true
	// only for a yes. ctx ends if the pager stops waiting first. Run needs
	// it; Offer and Set don't use it: the setup page's confirm is its own.
	Confirm func(ctx context.Context) (bool, error)

	// Log, when set, gets each of the pager's own serial lines while the
	// port is open (its log, never a provisioning line), without its line
	// ending, from the goroutine that reads the port; a line over 4,096
	// bytes is cut short. It never gets a line holding a password the
	// bridge has sent. nil for none (the setup page's pager channel).
	Log func(line string)
}

// The tool's waits (testdata/provisioning/README.md §Timing).
const (
	HelloTimeout     = 120 * time.Second // for a waiting prov_hello, after opening the port
	ConfirmedTimeout = 40 * time.Second  // for a confirmed prov_hello (BOOT), after the prov_offer: the device's 30 s, and 10
	SetWithin        = 30 * time.Second  // for the owner's confirmation on the Mac, after BOOT: the device's 30 s
	ResultTimeout    = 40 * time.Second  // for prov_result, after the prov_set
)

// Run pairs a pager over its serial port (parent 2.3–2.7; ADR 0011): it
// builds the prov_offer and the prov_set, opens the port, waits for the
// device's hello, prints both full fingerprints, and sends the prov_offer,
// which holds nothing secret. Once the pager reports BOOT, it asks the owner
// (o.Confirm) whether the fingerprints match; only on a yes does it send the
// prov_set with the networks, and it adds the device to devices.toml only
// when the device answers ok. Any other outcome is an error, and
// devices.toml is as it was. It's Offer, Confirmed, o.Confirm, then Set.
func Run(ctx context.Context, o Options) error {
	if o.Confirm == nil {
		return errors.New("provision: Run needs Options.Confirm, to ask the owner; devices.toml is unchanged")
	}
	o = o.withDefaults()
	o.warnNearMisses(o.Networks, "To use the Mac's, stop now (Ctrl-C) and give --wifi-ssid %q.")
	p, err := Offer(ctx, o)
	if err != nil {
		return err
	}
	defer p.Close()
	if err := p.Confirmed(ctx); err != nil {
		return err
	}
	fmt.Fprintf(o.Out, "The pager confirmed with BOOT. It waits %d s for its networks.\n", int(SetWithin/time.Second))
	if err := p.ask(ctx); err != nil {
		return err
	}
	return p.set(ctx, o.Networks)
}

// ask is the owner's confirmation on the Mac, through o.Confirm, while the
// pager waits for it.
func (p *Pairing) ask(ctx context.Context) error {
	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	answer := make(chan error, 1)
	go func() {
		ok, err := p.o.Confirm(actx)
		switch {
		case err != nil:
		case !ok:
			err = errors.New("the fingerprints weren't confirmed on this Mac, so no Wi-Fi password was sent. The pager stops waiting after 30 s, or press PWR on it now")
		}
		answer <- err
	}()
	select {
	case err := <-answer:
		if err != nil {
			return fmt.Errorf("%w; devices.toml is unchanged", err)
		}
		return nil
	case <-p.over:
		return p.ended()
	case <-ctx.Done():
		return fmt.Errorf("%w; devices.toml is unchanged", ctx.Err())
	}
}

func (o Options) withDefaults() Options {
	if o.Out == nil {
		o.Out = io.Discard
	}
	if o.After == nil {
		o.After = time.After
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	return o
}

// warnNearMisses tells the owner of each of nets that the Mac hasn't saved
// under that name but has under a near one (NearMisses), with advice, a
// format given the Mac's name, saying what to do about it.
func (o Options) warnNearMisses(nets []Network, advice string) {
	if o.KnownNetworks == nil {
		return
	}
	ssids := make([]string, len(nets))
	for i, n := range nets {
		ssids[i] = n.SSID
	}
	for _, m := range NearMisses(ssids, o.KnownNetworks()) {
		fmt.Fprintf(o.Out, "Warning: this Mac hasn't saved a Wi-Fi network named %q, but it has saved %q. The pager joins only the exact name. "+advice+"\n",
			m.Given, m.Known, m.Known)
	}
}

// A SettingsError is settings the pager wouldn't take: a name, host or
// network that breaks the contract (testdata/provisioning/README.md), or a
// prov_offer or prov_set over MaxLine bytes. Nothing was sent to the pager.
// It never quotes a password.
type SettingsError struct{ Err error }

func (e *SettingsError) Error() string { return "the settings for the pager: " + e.Err.Error() }
func (e *SettingsError) Unwrap() error { return e.Err }

// A Pairing is a pager that has said hello on its open port, with both full
// fingerprints shown and the prov_offer sent: nothing secret. It's Offer's
// result, for Set or Close, and the seam for a caller that has both
// fingerprints confirmed before it has the networks (the setup page, phase
// 5 task 3.5). Only Set sends a password, and only once the pager has
// reported BOOT on the offer.
type Pairing struct {
	o        Options // with its defaults, Host and hostName filled in
	hostName string
	devKey   ssh.PublicKey
	fw       string

	port *Port
	in   <-chan read   // the port's lines, which watch reads
	done chan struct{} // closed by Close: the port's reader and watch stop

	confirmed chan struct{} // closed when the pager reports BOOT on the offer
	sent      chan struct{} // closed when the prov_set has been written
	over      chan struct{} // closed when watch ends
	why       error         // why it ended (nil for the pager's ok), once over closes
	result    *Result       // the pager's answer, if that ended it

	mu      sync.Mutex
	state   int      // offered, setting or ended
	secrets [][]byte // the passwords sent, as written: log hides a line holding one
	closed  sync.Once
}

// hiddenLine stands in, for Options.Log, for a line holding a password.
const hiddenLine = "(a line holding a Wi-Fi password was hidden)"

// log gives o.Log one of the pager's log lines, unless it holds a password
// the bridge has sent: the pager never logs one, but a console that echoed
// its input would send the prov_set back (requirement 2.6).
func (p *Pairing) log(line []byte) {
	if p.o.Log == nil {
		return
	}
	p.mu.Lock()
	secrets := p.secrets
	p.mu.Unlock()
	for _, s := range secrets {
		if bytes.Contains(line, s) {
			p.o.Log(hiddenLine)
			return
		}
	}
	p.o.Log(string(line))
}

// secretsOf are nets' passwords, raw and as the prov_set's JSON writes them.
func secretsOf(nets []Network) [][]byte {
	var out [][]byte
	for _, n := range nets {
		if n.Pass == "" {
			continue
		}
		out = append(out, []byte(n.Pass))
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		enc.Encode(n.Pass)
		if q := bytes.TrimSuffix(b.Bytes(), []byte("\n")); len(q) > 2 && !bytes.Equal(q[1:len(q)-1], []byte(n.Pass)) {
			out = append(out, q[1:len(q)-1])
		}
	}
	return out
}

const (
	offered = iota
	setting
	ended
)

// DeviceKey is the pager's public key, from its prov_hello.
func (p *Pairing) DeviceKey() ssh.PublicKey { return p.devKey }

// HostKey is the bridge's host key, which the prov_offer carried.
func (p *Pairing) HostKey() ssh.PublicKey { return p.o.HostKey }

// FW is the pager's firmware version, from its prov_hello.
func (p *Pairing) FW() string { return p.fw }

// HostName is the Mac's name, which the pager shows beside the fingerprints.
func (p *Pairing) HostName() string { return p.hostName }

// Host is the address the pager will dial.
func (p *Pairing) Host() string { return p.o.Host }

// Name is the name the pager is paired under.
func (p *Pairing) Name() string { return p.o.Name }

// PagerConfirmed is closed when the pager reports BOOT on the offer.
func (p *Pairing) PagerConfirmed() <-chan struct{} { return p.confirmed }

// Over is closed when the pairing can't go on: the pager answered, one of
// the tool's waits ran out, the port failed, or Close was called. Err then
// says why.
func (p *Pairing) Over() <-chan struct{} { return p.over }

// Err is why the pairing is over, once Over is closed, ending "devices.toml
// is unchanged"; nil before, and for the pager's ok.
func (p *Pairing) Err() error {
	select {
	case <-p.over:
		if p.why == nil {
			return nil
		}
		return p.ended()
	default:
		return nil
	}
}

// ended is why the pairing is over, for its caller; over is closed.
func (p *Pairing) ended() error {
	if p.why == nil {
		return errors.New("this pairing has ended; start again")
	}
	return fmt.Errorf("%w; devices.toml is unchanged", p.why)
}

// Offer is pairing's first step. It checks the prov_offer it will send, and
// the prov_set too when o.Networks are given (Set checks them otherwise),
// opens the port, waits for the pager's hello, checks that devices.toml
// will take the pager under o.Name, prints both full fingerprints, and sends
// the prov_offer: the name, address, port, host key and the Mac's name,
// nothing secret. ctx bounds only the wait for the hello. On an error the
// port is closed and devices.toml is as it was.
func Offer(ctx context.Context, o Options) (*Pairing, error) {
	o = o.withDefaults()
	p, err := o.offer(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w; devices.toml is unchanged", err)
	}
	return p, nil
}

func (o Options) offer(ctx context.Context) (*Pairing, error) {
	hostName, err := o.LocalHostName()
	if err != nil {
		return nil, err
	}
	if o.Host == "" {
		o.Host = hostName + ".local"
	}
	offer, err := o.offerLine(hostName)
	if err != nil {
		return nil, err
	}
	if len(o.Networks) > 0 {
		if _, err := o.setLine(o.Networks); err != nil {
			return nil, err
		}
	}

	port, err := OpenPort(o.Port)
	if err != nil {
		return nil, err
	}
	p := &Pairing{o: o, hostName: hostName, port: port, done: make(chan struct{}),
		confirmed: make(chan struct{}), sent: make(chan struct{}), over: make(chan struct{})}
	p.in = readLines(port, p.done, p.log)
	ok := false
	defer func() {
		if !ok {
			p.Close()
		}
	}()

	fmt.Fprintf(o.Out, "Opened %s. Choose Pair again on the pager now (a pager that has never been paired is pairing already).\nWaiting up to %d s for it...\n",
		o.Port, int(HelloTimeout/time.Second))
	m, err := next(ctx, p.in, o.After(HelloTimeout), func(m Message) bool {
		h, ok := m.(*Hello)
		return ok && h.State == "waiting"
	})
	switch {
	case errors.Is(err, errTimedOut):
		return nil, &NoHelloError{Port: o.Port}
	case err != nil:
		return nil, err
	}
	hello := m.(*Hello)
	devKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(hello.PubKey))
	if err != nil {
		return nil, fmt.Errorf("the pager's key: %w", err)
	}
	if err := o.nameIsFree(devKey); err != nil {
		return nil, err
	}
	fmt.Fprintf(o.Out, "The pager's key:  %s (%s)\nThe bridge's key: %s (%s)\n",
		ssh.FingerprintSHA256(devKey), devKey.Type(), ssh.FingerprintSHA256(o.HostKey), o.HostKey.Type())
	p.devKey, p.fw = devKey, hello.FW
	if _, err := port.Write(append(offer, '\n')); err != nil {
		return nil, err
	}
	fmt.Fprintf(o.Out, "Sent the pager the bridge's key and this Mac's name, nothing secret. It shows PAIR WITH %s? and both keys: if they match these two, press BOOT on it to pair, or PWR to cancel. Waiting up to %d s...\n",
		hostName, int(ConfirmedTimeout/time.Second))
	go p.watch()
	ok = true
	return p, nil
}

// watch reads the pager's lines from the prov_offer on, until the pairing
// is over: it marks the pager's BOOT, and ends the pairing on the pager's
// answer, on a wait that runs out, or when the port fails or closes.
func (p *Pairing) watch() {
	defer close(p.over)
	bootBy := p.o.After(ConfirmedTimeout)
	var setBy, resultBy <-chan time.Time
	sent := p.sent
	booted, sending := false, false
	for {
		var err error
		select {
		case <-p.done:
			err = errors.New("the pairing was closed before it finished")
		case <-bootBy:
			err = fmt.Errorf("the pager didn't report BOOT within %d s, so no Wi-Fi password was sent", int(ConfirmedTimeout/time.Second))
		case <-setBy:
			p.mu.Lock()
			sending = p.state == setting
			p.mu.Unlock()
			if sending {
				setBy = nil
				continue // the prov_set went just now
			}
			err = fmt.Errorf("the pairing wasn't confirmed on this Mac within %d s of BOOT, so the pager stopped waiting and no Wi-Fi password was sent", int(SetWithin/time.Second))
		case <-sent:
			sent, setBy, sending = nil, nil, true
			resultBy = p.o.After(ResultTimeout)
			continue
		case <-resultBy:
			err = fmt.Errorf("no answer from the pager in %d s", int(ResultTimeout/time.Second))
		case l := <-p.in:
			m, rerr := parseRead(l)
			switch m := m.(type) {
			case nil:
				if rerr == nil {
					continue // a log line, or damage
				}
				err = rerr
			case *Hello:
				switch {
				case m.State == "confirmed" && !booted:
					booted, bootBy = true, nil
					setBy = p.o.After(SetWithin)
					close(p.confirmed)
					continue
				case m.State == "waiting" && booted && !sending:
					// Its timeout's answer was lost: it waits for an offer again.
					err = errors.New("the pager stopped waiting for its networks, so no Wi-Fi password was sent")
				default:
					continue
				}
			case *Result:
				p.result = m
				err = resultError(m, booted, sending)
			}
		}
		p.mu.Lock()
		if p.state == offered {
			p.state = ended
		}
		p.why = err
		p.mu.Unlock()
		return
	}
}

// resultError is what the pager's answer means for the owner, or nil for
// its ok to a prov_set (testdata/provisioning/README.md).
func resultError(r *Result, booted, sent bool) error {
	switch {
	case r.OK && sent:
		return nil
	case r.OK:
		return errors.New("the pager answered ok before it was sent its networks")
	case r.Err == "cancelled":
		return errors.New("pairing was cancelled on the pager (PWR)")
	case r.Err == "timeout" && !booted:
		return errors.New("nobody pressed BOOT on the pager within 30 s")
	case r.Err == "timeout":
		return errors.New("the pager stopped waiting: the pairing wasn't confirmed on this Mac within 30 s of BOOT, so no Wi-Fi password was sent")
	case r.Err == "invalid" && !booted:
		return errors.New("the pager refused the offer as invalid: its firmware is older than this bridge, which pairs in two steps. Flash the firmware shipped with this bridge first (the setup page's Flash step), then pair again")
	case r.Err == "invalid":
		return errors.New("the pager refused the settings as invalid")
	case r.Err == "busy":
		return errors.New("the pager is confirming another pairing; let it finish, then try again")
	case r.Err == "storage":
		return errors.New("the pager couldn't save the settings")
	}
	return fmt.Errorf("the pager answered %q", r.Err)
}

// Confirmed waits for the pager to report BOOT on the offer, and returns
// nil; or why it won't: the pager answered (PWR, its 30 s, an older pager's
// invalid), a wait ran out, the pairing closed, or ctx ended. It sends
// nothing.
func (p *Pairing) Confirmed(ctx context.Context) error {
	select {
	case <-p.confirmed:
		select {
		case <-p.over:
			return p.ended()
		default:
			return nil
		}
	case <-p.over:
		return p.ended()
	case <-ctx.Done():
		return fmt.Errorf("%w; devices.toml is unchanged", ctx.Err())
	}
}

// Check returns a *SettingsError if nets aren't settings the pager would
// take, and sends nothing.
func (p *Pairing) Check(nets []Network) error {
	_, err := p.o.setLine(nets)
	return err
}

// Set is pairing's second step, and the caller's confirmation that both
// fingerprints match: it waits for the pager's BOOT on the offer if it
// hasn't come, sends the prov_set with nets and the offer's host key, waits
// for the pager's answer, and adds the device to devices.toml only when the
// pager says ok. It first warns of each near-miss name (KnownNetworks). If
// nets aren't settings the pager would take, it sends nothing and returns a
// *SettingsError, and the pairing waits for others. Otherwise it ends the
// pairing, whatever the outcome; on any but ok devices.toml is as it was.
func (p *Pairing) Set(ctx context.Context, nets []Network) error {
	if err := p.Check(nets); err != nil {
		return err
	}
	p.o.warnNearMisses(nets, "If you meant the Mac's, pair again with %q once this pairing ends.")
	return p.set(ctx, nets)
}

// set is Set, warned already. It ends the pairing unless nets are settings
// the pager wouldn't take.
func (p *Pairing) set(ctx context.Context, nets []Network) error {
	line, err := p.o.setLine(nets)
	if err != nil {
		return err
	}
	defer p.Close()
	if err := p.Confirmed(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	state := p.state
	if state == offered {
		p.state = setting
		p.secrets = secretsOf(nets)
	}
	p.mu.Unlock()
	if state != offered {
		<-p.over
		return p.ended()
	}

	fmt.Fprintf(p.o.Out, "Both confirmed: sending the pager its Wi-Fi networks.\n")
	if _, err := p.port.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("%w; devices.toml is unchanged", err)
	}
	close(p.sent)
	select {
	case <-p.over:
	case <-ctx.Done():
		return fmt.Errorf("%w; devices.toml is unchanged", ctx.Err())
	}
	if p.why != nil || p.result == nil {
		return p.ended()
	}
	o := p.o
	d := devices.Device{
		Name:      o.Name,
		Key:       strings.TrimSpace(string(ssh.MarshalAuthorizedKey(p.devKey))) + " " + o.Name,
		PublicKey: p.devKey,
		Added:     o.Now().UTC().Truncate(time.Second),
	}
	if err := devices.Add(o.Devices, d); err != nil {
		return err
	}
	fmt.Fprintf(o.Out, "Paired %s.\n", o.Name)
	return nil
}

// Close ends the pairing and closes the port. Before Set, nothing secret
// has been sent to the pager, and devices.toml is as it was. It's safe to
// call more than once, and after Set.
func (p *Pairing) Close() error {
	var err error
	p.closed.Do(func() {
		p.mu.Lock()
		if p.state == offered {
			p.state = ended
		}
		p.mu.Unlock()
		close(p.done)
		err = p.port.Close()
	})
	return err
}

// errTimedOut is a wait that ran out.
var errTimedOut = errors.New("timed out")

// nameIsFree checks, before the device is sent anything, that devices.toml
// will take it: it loads, and o.Name is free or already this key's. Adding
// the device after it saved its settings must not fail on either. The same
// key under any name is re-pairing: its entry is updated (parent 16.3).
func (o Options) nameIsFree(key ssh.PublicKey) error {
	devs, err := devices.Load(o.Devices)
	if err != nil {
		return err
	}
	if own, ok := devices.Match(devs, key); ok && own.Name == o.Name {
		return nil
	}
	for _, d := range devs {
		if d.Name == o.Name {
			return &NameTakenError{Name: o.Name, Fingerprint: d.Fingerprint()}
		}
	}
	return nil
}

// NoHelloError is a pager that said no hello in HelloTimeout: one that
// isn't in pairing mode, or another port. Nothing was sent to it. Its
// message is the CLI's; the setup page words it for itself.
type NoHelloError struct{ Port string }

func (e *NoHelloError) Error() string {
	return fmt.Sprintf("no hello from the pager in %d s. Choose Pair again in the pager's menu, then run provision again (a pager showing PAIR VIA USB is pairing already: check --port and the cable)",
		int(HelloTimeout/time.Second))
}

// A NameTakenError is a pairing under a name that devices.toml holds with
// another key, Fingerprint. Its text is the CLI's; the setup page says it in
// its own words, and offers to replace that device (the owner's checkpoint
// 7.2, 2026-11-07).
type NameTakenError struct{ Name, Fingerprint string }

func (e *NameTakenError) Error() string {
	return fmt.Sprintf("a device named %s is already paired with another key (%s). Revoke it first with `pager-bridge devices revoke %s`, or choose another --name",
		e.Name, e.Fingerprint, e.Name)
}

// offerLine is the prov_offer line for o, without its newline: everything
// but the networks.
func (o Options) offerLine(hostName string) ([]byte, error) {
	return toolLine(struct {
		T        string `json:"t"`
		Name     string `json:"name"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		HostKey  string `json:"host_key"`
		HostName string `json:"host_name"`
	}{"prov_offer", o.Name, o.Host, o.BridgePort, o.hostKeyText(), hostName})
}

// setLine is the prov_set line for nets, without its newline: the offer's
// host key and the networks.
func (o Options) setLine(nets []Network) ([]byte, error) {
	type network struct {
		SSID string `json:"ssid"`
		Pass string `json:"pass"`
	}
	s := struct {
		T       string    `json:"t"`
		HostKey string    `json:"host_key"`
		WiFi    []network `json:"wifi"`
	}{T: "prov_set", HostKey: o.hostKeyText()}
	for _, n := range nets {
		s.WiFi = append(s.WiFi, network{n.SSID, n.Pass})
	}
	return toolLine(s)
}

func (o Options) hostKeyText() string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(o.HostKey)))
}

// toolLine is v as a provisioning line, without its newline. It must keep
// the contract the device checks it against, so it's parsed as the device
// would; a line that breaks it is a *SettingsError.
func toolLine(v any) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(Prefix)
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	line := bytes.TrimSuffix(b.Bytes(), []byte("\n"))
	if _, err := ParseTool(line); err != nil {
		return nil, &SettingsError{err}
	}
	return line, nil
}

// A read is one line from the port, without its line ending, or why there
// are no more.
type read struct {
	line []byte
	err  error
}

// maxRead is the longest line kept whole; a longer log line is cut short.
const maxRead = 4096

// readLines reads the port's lines until it fails or done closes. It gives
// log its log lines, and sends the rest: provisioning lines, whole or
// damaged, and why it stopped.
func readLines(r io.Reader, done <-chan struct{}, log func([]byte)) <-chan read {
	in := make(chan read)
	go func() {
		br := bufio.NewReaderSize(r, maxRead)
		for {
			line, err := br.ReadSlice('\n')
			long := errors.Is(err, bufio.ErrBufferFull)
			l := read{line: append([]byte(nil), line...)}
			for errors.Is(err, bufio.ErrBufferFull) {
				_, err = br.ReadSlice('\n')
			}
			if !long {
				l.line = bytes.TrimSuffix(bytes.TrimSuffix(l.line, []byte("\n")), []byte("\r"))
			}
			if err != nil {
				l = read{err: err}
			} else if !bytes.HasPrefix(l.line, []byte(Prefix)) {
				log(l.line)
				continue
			}
			select {
			case in <- l:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return in
}

// parseRead is the message l holds; nil and no error for a log line or
// damage. A provisioning line that isn't one JSON value, or is too long, is
// damage, not a message: a pager's USB console that nothing read for a while
// delivers lines cut short or run together (the desk checkpoint,
// 2026-10-24), so it's passed over, and the pager's next hello or result is
// read. A whole line that breaks the contract is an error naming what's
// wrong with it.
func parseRead(l read) (Message, error) {
	if l.err != nil {
		return nil, fmt.Errorf("reading the port: %w", l.err)
	}
	if !bytes.HasPrefix(l.line, []byte(Prefix)) {
		return nil, nil // a log line
	}
	m, err := ParseDevice(l.line)
	var e *Error
	if errors.As(err, &e) && (e.Field == "json" || e.Field == "line") {
		return nil, nil // damage
	}
	if err != nil {
		return nil, fmt.Errorf("the pager sent an invalid line: %w", err)
	}
	return m, nil
}

// next returns the next provisioning message that want accepts, passing
// over log lines, damage (parseRead) and the messages it doesn't want.
func next(ctx context.Context, in <-chan read, timeout <-chan time.Time, want func(Message) bool) (Message, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			return nil, errTimedOut
		case l := <-in:
			m, err := parseRead(l)
			if err != nil {
				return nil, err
			}
			if m != nil && want(m) {
				return m, nil
			}
		}
	}
}

// A Port is the pager's serial port, opened raw with termios: there is no
// serial library.
type Port struct {
	f     *os.File
	saved unix.Termios // restored on Close
}

// OpenPort opens and configures the serial port at path. It's opened
// non-blocking, so it never waits for a carrier, and reads and writes go
// through Go's poller.
func OpenPort(path string) (*Port, error) {
	f, err := os.OpenFile(path, os.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	p := &Port{f: f}
	if err := p.control(func(fd int) (err error) { p.saved, err = makeRaw(fd); return err }); err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return p, nil
}

func (p *Port) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *Port) Write(b []byte) (int, error) { return p.f.Write(b) }

// Close restores the port's settings and closes it.
func (p *Port) Close() error {
	err := p.control(func(fd int) error { return unix.IoctlSetTermios(fd, unix.TIOCSETA, &p.saved) })
	return errors.Join(err, p.f.Close())
}

// control runs fn on the port's descriptor without making it blocking, as
// File.Fd would.
func (p *Port) control(fn func(fd int) error) error {
	rc, err := p.f.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := rc.Control(func(fd uintptr) { ferr = fn(int(fd)) }); err != nil {
		return err
	}
	return ferr
}

// makeRaw puts the terminal fd in raw mode, 8N1 at 115,200 baud with no flow
// control, as cfmakeraw does, and returns its settings before.
func makeRaw(fd int) (saved unix.Termios, err error) {
	t, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return unix.Termios{}, err
	}
	saved = *t
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON | unix.IXOFF | unix.IXANY
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CSIZE | unix.PARENB | unix.CSTOPB | unix.CRTSCTS
	t.Cflag |= unix.CS8 | unix.CREAD | unix.CLOCAL
	t.Cc[unix.VMIN], t.Cc[unix.VTIME] = 1, 0
	t.Ispeed, t.Ospeed = unix.B115200, unix.B115200
	return saved, unix.IoctlSetTermios(fd, unix.TIOCSETA, t)
}
