package doctor

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/config"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/status"
)

// The checks' ids, in the order Run reports them, each with its states.
const (
	CheckHerdr    = "herdr"    // herdr's socket: ok, missing, not-socket, refused, error
	CheckKeys     = "keys"     // the host key and config.toml: ok, missing, invalid
	CheckPort     = "port"     // the bridge's port: bridge, free, taken, unknown
	CheckFirewall = "firewall" // the macOS firewall: off, allowing, blocking, block-all, unknown
	CheckLogin    = "login"    // run at login: brew, launchd, other, off, error, both, none, unknown
	CheckDevices  = "devices"  // the paired devices: connected, disconnected, not-running, none, error
)

// FirewallTool is the macOS application firewall's command-line tool. The
// doctor asks it two questions (in run, which writes the path out, as the
// repository's check of launches needs); the fixes it prints use it, with
// sudo, to allow the bridge.
const FirewallTool = "/usr/libexec/ApplicationFirewall/socketfilterfw"

// Formula is herdr-pager's Homebrew formula, whose service runs `pager-bridge
// serve` at login (design D5P-7).
const Formula = "herdr-pager"

// LaunchdLabel is the launchd agent that runs a source build at login
// (contrib/launchd, docs/bridge.md §Run it at login).
const LaunchdLabel = "dev.herdr-pager.bridge"

// Env is what the checks read, passed in so that tests never reach the
// owner's home or herdr.
type Env struct {
	Home       string              // the user's home; the bridge's directory is under it
	Getenv     func(string) string // for $HERDR_SOCKET_PATH; nil reads none
	Now        time.Time           // to tell whether status.json is a running bridge's
	Executable string              // the bridge's own path, symlinks resolved: the firewall's question
	// Dial connects to herdr's socket ("unix") and to the bridge's port
	// ("tcp"), to see whether something answers; nil is net.DialTimeout
	// with a 1 s limit.
	Dial func(network, address string) (net.Conn, error)
}

// A runner runs the command argv, whose words are always fixed, and returns
// what it printed on stdout. A command that isn't there is an error wrapping
// exec.ErrNotFound or fs.ErrNotExist.
type runner func(argv ...string) ([]byte, error)

// A Check is one check's verdict.
type Check struct {
	ID       string   `json:"id"`                 // one of the Check constants
	Name     string   `json:"name"`               // what it checks, for people
	OK       bool     `json:"ok"`                 // whether it passed
	State    string   `json:"state"`              // what it found, as one word (the constants list them)
	Detail   string   `json:"detail"`             // what it found, for people
	Fix      string   `json:"fix,omitempty"`      // how to fix it, when it fails
	Commands []string `json:"commands,omitempty"` // the shell commands that would, when there are any
	Devices  []Device `json:"devices,omitempty"`  // the paired devices (the devices check only)
}

// A Device is one paired pager, as the devices check sees it.
type Device struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"` // its key's full SHA256 fingerprint
	Connected   bool   `json:"connected"`   // whether a running bridge has its session open
}

// A Report is every check, in order, and whether all passed.
type Report struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

// Run runs every check in env, with the real socketfilterfw and brew. It
// gives each command 20 s.
func Run(env Env) Report {
	return run(env, func(argv ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		c := exec.CommandContext(ctx, argv[0], argv[1:]...)
		c.Env = append(os.Environ(), "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ENV_HINTS=1")
		return c.Output()
	})
}

// run is Run, given how to run a command. Every command the doctor runs is
// called here, its words written out, so the repository's check of process
// launches (internal/repocheck, parent 13.3) can see each one: the firewall's
// state and its list of apps, and Homebrew's list of services, by brew on
// PATH or where Homebrew puts it on Apple Silicon.
func run(env Env, cmd runner) Report {
	if env.Getenv == nil {
		env.Getenv = func(string) string { return "" }
	}
	if env.Dial == nil {
		env.Dial = func(network, address string) (net.Conn, error) {
			return net.DialTimeout(network, address, time.Second)
		}
	}
	dir := config.Dir(env.Home)
	cfg, cfgErr := config.Load(filepath.Join(dir, config.File), config.Env{Home: env.Home, Getenv: env.Getenv})
	if cfgErr != nil { // the other checks still run, on the defaults
		cfg = config.Config{Listen: config.DefaultListen, HerdrSocket: env.Getenv("HERDR_SOCKET_PATH")}
		if cfg.HerdrSocket == "" {
			cfg.HerdrSocket = filepath.Join(env.Home, ".config", "herdr", "herdr.sock")
		}
	}
	st, running, err := status.Read(dir, env.Now)
	if err != nil { // a status.json that won't parse isn't a running bridge's
		running = false
	}

	var fw firewall
	fw.state, fw.stateErr = cmd("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate")
	if globalState(fw.state, fw.stateErr) == 1 { // on: does it block all, or the bridge?
		fw.blockAll, fw.blockAllErr = cmd("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getblockall")
		if env.Executable != "" {
			fw.app, fw.appErr = cmd("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getappblocked", ownPath(env))
		}
	}
	brew, brewErr := cmd("brew", "services", "list", "--json")
	if missing(brewErr) {
		brew, brewErr = cmd("/opt/homebrew/bin/brew", "services", "list", "--json")
	}

	login := checkLogin(env.Home, env.Executable, brew, brewErr)
	r := Report{Checks: []Check{
		checkHerdr(env, cfg.HerdrSocket),
		checkKeys(dir, cfgErr),
		checkPort(env, cfg.Listen, st, running),
		checkFirewall(fw, env.Executable),
		login,
		checkDevices(dir, st, running, login.State),
	}}
	r.OK = !slices.ContainsFunc(r.Checks, func(c Check) bool { return !c.OK })
	return r
}

func pass(c Check, state, detail string) Check {
	c.OK, c.State, c.Detail = true, state, detail
	return c
}

func fail(c Check, state, detail, fix string, commands ...string) Check {
	c.OK, c.State, c.Detail, c.Fix, c.Commands = false, state, detail, fix, commands
	return c
}

// checkHerdr checks that herdr answers on its socket, sock.
func checkHerdr(env Env, sock string) Check {
	c := Check{ID: CheckHerdr, Name: "herdr's socket"}
	fi, err := os.Stat(sock)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fail(c, "missing", fmt.Sprintf("no socket at %s: herdr isn't running, or its socket is elsewhere", sock),
			"Start herdr. If its socket is somewhere else, set herdr_socket in ~/.config/herdr-pager/config.toml to its path.")
	case err != nil:
		return fail(c, "error", err.Error(),
			"Check that you can read the socket's folder, or set herdr_socket in ~/.config/herdr-pager/config.toml to herdr's socket.")
	case fi.Mode()&fs.ModeSocket == 0:
		return fail(c, "not-socket", sock+" isn't a socket", "Set herdr_socket in ~/.config/herdr-pager/config.toml to herdr's socket.")
	}
	conn, err := env.Dial("unix", sock)
	if err != nil {
		return fail(c, "refused", fmt.Sprintf("nothing answers at %s: herdr has stopped", sock), "Start herdr.")
	}
	conn.Close()
	return pass(c, "ok", "herdr answers at "+sock)
}

// checkKeys checks that the bridge's directory holds a host key that loads,
// and a config.toml that loads (cfgErr is its error).
func checkKeys(dir string, cfgErr error) Check {
	c := Check{ID: CheckKeys, Name: "The bridge's keys"}
	pub, err := config.HostKey(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fail(c, "missing", fmt.Sprintf("no host key (%s) in %s", config.HostKeyFile, dir),
			"Make it with the command below, or open the setup page, which makes it for you.", "pager-bridge init")
	case err != nil:
		return fail(c, "invalid", err.Error(),
			"The host key won't load. Move it aside and run `pager-bridge init` for a new one; then pair each pager again, since each knows the bridge by its key.")
	case errors.Is(cfgErr, fs.ErrNotExist):
		return fail(c, "missing", fmt.Sprintf("no %s in %s", config.File, dir),
			"Make it with the command below (it keeps the host key), or open the setup page, which makes it for you.", "pager-bridge init")
	case cfgErr != nil:
		return fail(c, "invalid", cfgErr.Error(),
			"Fix that line of config.toml (docs/bridge.md lists the settings), or delete the file and run `pager-bridge init` for the defaults.")
	}
	return pass(c, "ok", fmt.Sprintf("the host key is %s; %s loads", ssh.FingerprintSHA256(pub), config.File))
}

// checkPort checks that the bridge holds its port, listen, or that the port
// is free for it. It dials the port rather than binding it, so it never takes
// the port from a bridge that's starting, nor makes the firewall ask about
// the doctor.
func checkPort(env Env, listen string, st status.Status, running bool) Check {
	host, port, err := net.SplitHostPort(listen)
	if err != nil { // config.Load checked it, and the default is good
		host, port = "", "2277"
	}
	c := Check{ID: CheckPort, Name: "Port " + port}
	if running {
		return pass(c, "bridge", "the bridge listens on "+st.Listening)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1" // a listener on every address answers here
	}
	lsof := "lsof -nP -iTCP:" + port + " -sTCP:LISTEN"
	conn, err := env.Dial("tcp", net.JoinHostPort(host, port))
	switch {
	case err == nil:
		conn.Close()
		return fail(c, "taken", fmt.Sprintf("another process listens on port %s, so the bridge can't (%s)", port, listen),
			"Find it with the command below, and stop it. If it's another pager-bridge, such as one started in a terminal, stop that: one bridge serves every pager.", lsof)
	case errors.Is(err, syscall.ECONNREFUSED):
		return pass(c, "free", fmt.Sprintf("free for the bridge (%s); no bridge is running", listen))
	}
	return fail(c, "unknown", fmt.Sprintf("couldn't tell whether port %s is free: %v", port, err),
		"See whether a process listens on it with the command below.", lsof)
}

// A firewall is what socketfilterfw said: its global state, and, while it's
// on, its block all state and its verdict on the bridge's own path. It isn't
// asked for its list of apps: on macOS 26.6.2 that never lists an unsigned
// binary allowed with --add and --unblockapp (the owner's checkpoint 7.2,
// 2026-11-07).
type firewall struct {
	state, blockAll, app          []byte
	stateErr, blockAllErr, appErr error
}

// ownPath is the one argument of a launch that isn't a literal: the
// firewall's question about the bridge's own path, env.Executable, which
// System resolved from os.Executable. internal/repocheck allows it only as
// the direct result of this function, in this package.
func ownPath(env Env) string { return env.Executable }

// The firewall's global state, as `socketfilterfw --getglobalstate` prints
// it: "Firewall is enabled. (State = 1)".
var stateRE = regexp.MustCompile(`State = (\d+)`)

// globalState is the firewall's state from what --getglobalstate printed: 0
// off, 1 on, 2 blocking every incoming connection, or -1 unknown.
func globalState(out []byte, err error) int {
	if err != nil {
		return -1
	}
	text := string(out)
	if m := stateRE.FindStringSubmatch(text); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	switch {
	case strings.Contains(text, "disabled"):
		return 0
	case strings.Contains(text, "block all"):
		return 2
	case strings.Contains(text, "enabled"):
		return 1
	}
	return -1
}

// verdict returns what `socketfilterfw --getappblocked <exe>` printed, out,
// says of exe: "allow" for "Incoming connection to <exe> is permitted.",
// "block" for "… is blocked.", or "" for anything else (an app that isn't
// part of the firewall, or another path).
func verdict(out []byte, exe string) string {
	line := strings.TrimSpace(firstLine(strings.TrimSpace(string(out))))
	switch line {
	case "Incoming connection to " + exe + " is permitted.":
		return "allow"
	case "Incoming connection to " + exe + " is blocked.":
		return "block"
	}
	return ""
}

// checkFirewall checks that the macOS firewall is off, or lets the bridge's
// own path, exe, take incoming connections.
func checkFirewall(fw firewall, exe string) Check {
	c := Check{ID: CheckFirewall, Name: "The firewall"}
	unknown := func(detail string) Check {
		return fail(c, "unknown", detail,
			"Look in System Settings → Network → Firewall. If it's on, allow pager-bridge there (Options…), and turn off Block all incoming connections.")
	}
	switch globalState(fw.state, fw.stateErr) {
	case 0:
		return pass(c, "off", "off: it lets the pagers' calls through")
	case 1:
	case 2:
		return fail(c, "block-all", "on, and it blocks all incoming connections, the pagers' calls among them",
			"In System Settings → Network → Firewall → Options…, turn off Block all incoming connections. Then run the doctor again: the bridge may still need allowing.")
	default:
		if fw.stateErr != nil {
			return unknown(fmt.Sprintf("couldn't read the firewall's state: %v", fw.stateErr))
		}
		return unknown(fmt.Sprintf("couldn't read the firewall's state from %q", firstLine(strings.TrimSpace(string(fw.state)))))
	}

	if fw.blockAllErr == nil && strings.Contains(string(fw.blockAll), "block all state set to enabled") {
		return fail(c, "block-all", "on, and it blocks all incoming connections, the pagers' calls among them",
			"In System Settings → Network → Firewall → Options…, turn off Block all incoming connections. Then run the doctor again: the bridge may still need allowing.")
	}
	switch {
	case exe == "":
		return unknown("on, and the bridge's own path is unknown, so the doctor can't see whether it's allowed")
	case fw.appErr != nil:
		return unknown(fmt.Sprintf("on; couldn't ask it about %s: %v", exe, fw.appErr))
	}
	fix := "Allow it, as an administrator, with the commands below (each asks for your password). An update moves the bridge to a new path, which needs allowing again."
	allow := []string{"sudo " + FirewallTool + " --add " + shellQuote(exe), "sudo " + FirewallTool + " --unblockapp " + shellQuote(exe)}
	switch verdict(fw.app, exe) {
	case "allow":
		return pass(c, "allowing", "on, and it allows "+exe)
	case "block":
		return fail(c, "blocking", fmt.Sprintf("on, and it blocks %s, so the pagers' calls never arrive", exe), fix, allow...)
	}
	return fail(c, "blocking", fmt.Sprintf("on, and it doesn't permit %s (it says %q), so the pagers' calls never arrive", exe, firstLine(strings.TrimSpace(string(fw.app)))), fix, allow...)
}

// shellQuote is s as one word for a shell: as it is if that's safe, else in
// single quotes.
func shellQuote(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%_+=:,./-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// A brewService is one entry of `brew services list --json`.
type brewService struct {
	Name     string `json:"name"`
	Status   string `json:"status"`    // started, none, stopped, error, …
	ExitCode *int   `json:"exit_code"` // its last exit, when it has one
}

// missing is whether err says a command isn't there.
func missing(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist)
}

// checkLogin checks that one thing runs the bridge at login: Homebrew's
// service (a Homebrew install; out and brewErr are `brew services list
// --json`'s) or the launchd agent (a source build) in home, running this
// bridge, exe.
func checkLogin(home, exe string, out []byte, brewErr error) Check {
	c := Check{ID: CheckLogin, Name: "Run at login"}
	plist := filepath.Join(home, "Library", "LaunchAgents", LaunchdLabel+".plist")
	_, err := os.Stat(plist)
	agent := err == nil

	var svc *brewService
	switch {
	case missing(brewErr):
		brewErr = nil // no Homebrew
	case brewErr == nil:
		var list []brewService
		if err := json.Unmarshal(out, &list); err != nil {
			brewErr = fmt.Errorf("brew services list printed what isn't its JSON: %w", err)
		}
		if i := slices.IndexFunc(list, func(s brewService) bool { return s.Name == Formula }); i >= 0 {
			svc = &list[i]
		}
	}

	switch {
	case svc != nil && svc.Status == "started" && agent:
		return fail(c, "both", fmt.Sprintf("brew services runs %s at login, and so does the launchd agent %s: two bridges for one port", Formula, LaunchdLabel),
			"Keep one: for a Homebrew install, Homebrew's. Remove the launchd agent, left from a build from source, with the commands below.",
			"launchctl bootout gui/$(id -u)/"+LaunchdLabel, "rm ~/Library/LaunchAgents/"+LaunchdLabel+".plist")
	case svc != nil && svc.Status == "started":
		return pass(c, "brew", fmt.Sprintf("brew services runs %s at login", Formula))
	case svc != nil && svc.Status == "error":
		detail := fmt.Sprintf("brew services runs %s at login, but it failed", Formula)
		if svc.ExitCode != nil {
			detail = fmt.Sprintf("brew services runs %s at login, but it exited with code %d", Formula, *svc.ExitCode)
		}
		return fail(c, "error", detail, "The bridge's log says why (the setup page shows it). Fix that, then restart it with the command below.",
			"brew services restart "+Formula)
	case agent:
		return checkAgent(c, plist, exe)
	case svc != nil:
		return fail(c, "off", fmt.Sprintf("%s is installed with Homebrew, but brew services doesn't run it at login (it says %q)", Formula, svc.Status),
			"Turn on Run at login on the setup page, or with the command below.", "brew services start "+Formula)
	}
	none := "Installed with Homebrew: `brew services start " + Formula + "`. Built from source: install the launchd agent, as docs/bridge.md (Run it at login) says."
	if brewErr != nil {
		return fail(c, "unknown", fmt.Sprintf("no launchd agent, and brew services couldn't say: %v", brewErr), none)
	}
	return fail(c, "none", "nothing runs the bridge at login: neither brew services nor a launchd agent", none)
}

// checkAgent checks that the launchd agent, plist, runs this bridge, exe (the
// owner's second recheck of checkpoint 7.2, 2026-11-07: the agent ran rc.3's
// serve, and rc.4's doctor said it ran "the bridge"). With exe unknown, it
// says which bridge the agent runs.
func checkAgent(c Check, plist, exe string) Check {
	switchFix := "Switch to this bridge with Run at login on the setup page (pager-bridge setup): it rewrites the agent to run this one, " +
		"and gives the commands that reload it. To keep the other bridge, use its own setup page and doctor instead."
	program, err := LaunchdProgram(plist)
	switch {
	case err != nil:
		return fail(c, "other", fmt.Sprintf("the launchd agent %s is there, but the doctor couldn't read which bridge it runs: %v", LaunchdLabel, err), switchFix)
	case exe == "":
		return pass(c, "launchd", fmt.Sprintf("the launchd agent %s runs %s serve at login", LaunchdLabel, program))
	case SameProgram(program, exe):
		return pass(c, "launchd", fmt.Sprintf("the launchd agent %s runs this bridge, %s, at login", LaunchdLabel, exe))
	}
	return fail(c, "other", fmt.Sprintf("the launchd agent %s runs another bridge at login: %s serve, not this one (%s)", LaunchdLabel, program, exe), switchFix)
}

// LaunchdProgram is the program the launchd agent's plist, at path, runs:
// its Program, or else the first of its ProgramArguments (launchd.plist(5)),
// from the plist's top-level dict. It reads XML plists, as contrib/launchd's
// and the setup page's are.
func LaunchdProgram(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	type node struct {
		XMLName xml.Name
		Text    string `xml:",chardata"`
		Nodes   []node `xml:",any"`
	}
	var p struct {
		Dict node `xml:"dict"`
	}
	if err := xml.Unmarshal(data, &p); err != nil {
		return "", fmt.Errorf("%s isn't an XML plist: %w", path, err)
	}
	var program, args string
	kids := p.Dict.Nodes
	for i := 0; i+1 < len(kids); i++ {
		k, v := kids[i], kids[i+1]
		if k.XMLName.Local != "key" {
			continue
		}
		switch strings.TrimSpace(k.Text) {
		case "Program":
			if v.XMLName.Local == "string" {
				program = v.Text
			}
		case "ProgramArguments":
			if v.XMLName.Local == "array" && len(v.Nodes) > 0 && v.Nodes[0].XMLName.Local == "string" {
				args = v.Nodes[0].Text
			}
		}
	}
	if program == "" {
		program = args
	}
	if program == "" {
		return "", fmt.Errorf("%s names no program", path)
	}
	return program, nil
}

// SameProgram is whether program, as a launchd agent names it, is exe, the
// bridge's own path with its symlinks resolved: the same path, or a symlink to
// it.
func SameProgram(program, exe string) bool {
	if program == exe {
		return true
	}
	resolved, err := filepath.EvalSymlinks(program)
	return err == nil && resolved == exe
}

// checkDevices checks that a device is paired, and that a running bridge has
// each one connected. login is the login check's state, which says how to
// start the bridge.
func checkDevices(dir string, st status.Status, running bool, login string) Check {
	c := Check{ID: CheckDevices, Name: "Paired pagers"}
	devs, err := devices.Load(filepath.Join(dir, devices.File))
	if err != nil {
		return fail(c, "error", err.Error(), "Fix that line of devices.toml, or pair the pager again on the setup page.")
	}
	if len(devs) == 0 {
		return fail(c, "none", "no pager is paired", "Pair one on the setup page.", "pager-bridge setup")
	}
	var missing []string
	for _, d := range devs {
		on := running && slices.Contains(st.Connected, d.Name)
		c.Devices = append(c.Devices, Device{Name: d.Name, Fingerprint: d.Fingerprint(), Connected: on})
		if !on {
			missing = append(missing, d.Name)
		}
	}
	paired := fmt.Sprintf("%d paired", len(devs))
	if !running {
		detail := paired + ", but no bridge is running, so none can connect"
		switch login {
		case "brew", "error", "both":
			return fail(c, "not-running", detail,
				"brew services runs the bridge at login, yet it isn't running: its log says why (the setup page shows it). Fix that, then restart it with the command below.",
				"brew services restart "+Formula)
		case "off":
			return fail(c, "not-running", detail, "Start it, and run it at login, with the command below, or on the setup page.", "brew services start "+Formula)
		case "launchd":
			return fail(c, "not-running", detail,
				"The launchd agent runs the bridge, yet it isn't running: its log, ~/Library/Logs/herdr-pager/bridge.log, says why. Fix that, then start it again with the command below.",
				"launchctl kickstart -k gui/$(id -u)/"+LaunchdLabel)
		}
		return fail(c, "not-running", detail, "Start it in a terminal with the command below, or have it run at login (the login check says how).", "pager-bridge serve")
	}
	if len(missing) > 0 {
		verb := " isn't connected"
		if len(missing) > 1 {
			verb = " aren't connected"
		}
		return fail(c, "disconnected", strings.Join(missing, ", ")+verb,
			"Check that each is on, and on a Wi-Fi network the Mac is on: its screen says why it can't connect. If the firewall check fails, fix that first.")
	}
	return pass(c, "connected", paired+", all connected")
}

// WriteText prints r for people: each check's verdict and what it found, the
// paired devices, each failing check's fix and commands, and how many fail.
func (r Report) WriteText(w io.Writer) error {
	width := 0
	for _, c := range r.Checks {
		width = max(width, len(c.Name))
	}
	var b strings.Builder
	failed := 0
	for _, c := range r.Checks {
		verdict := "ok  "
		if !c.OK {
			verdict = "FAIL"
			failed++
		}
		fmt.Fprintf(&b, "%s  %-*s  %s\n", verdict, width, c.Name, c.Detail)
		names := 0
		for _, d := range c.Devices {
			names = max(names, len(d.Name))
		}
		for _, d := range c.Devices {
			state := "not connected"
			if d.Connected {
				state = "connected    "
			}
			fmt.Fprintf(&b, "      %-*s  %s  %s\n", names, d.Name, state, d.Fingerprint)
		}
		if c.Fix != "" {
			fmt.Fprintf(&b, "      Fix: %s\n", c.Fix)
		}
		for _, cmd := range c.Commands {
			fmt.Fprintf(&b, "        %s\n", cmd)
		}
	}
	switch failed {
	case 0:
		fmt.Fprintf(&b, "\nEvery check passes.\n")
	case 1:
		fmt.Fprintf(&b, "\n1 check of %d fails.\n", len(r.Checks))
	default:
		fmt.Fprintf(&b, "\n%d checks of %d fail.\n", failed, len(r.Checks))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// System is this Mac's Env: the user's home, the environment, the clock, and
// the bridge's own path with its symlinks resolved (the path the firewall
// knows). Without its own path, the firewall check says it can't ask.
func System() (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		exe = ""
	}
	return Env{Home: home, Getenv: os.Getenv, Now: time.Now(), Executable: exe}, nil
}
