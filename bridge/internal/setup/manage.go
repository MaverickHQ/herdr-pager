package setup

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/config"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/doctor"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/status"
)

// The page's Manage view (requirement 2.3; design §The setup page, Manage),
// after setup:
//
//   - GET /api/devices: the paired devices, each with its full fingerprint,
//     and whether a running bridge has its session open.
//   - POST /api/devices/revoke {name}: revokes one, through internal/devices.
//     Pairing again is pairing (pair.go): the same key under the same name
//     updates its entry.
//   - The pager's fw, from its hello, against the shipped firmware's
//     version: POST /api/pair's firmware (pair.go), and GET /api/firmware.
//   - POST /api/login {on}: run at login, on or off, by brew services with
//     fixed arguments.
//   - GET /api/doctor: the bridge's checks, the doctor's report (D5P-6).

// devicesList is GET /api/devices.
func (h *Handler) devicesList(w http.ResponseWriter, r *http.Request) {
	if h.opts.Home == "" {
		jsonError(w, http.StatusConflict, "the setup page wasn't given the bridge's directory")
		return
	}
	dir := config.Dir(h.opts.Home)
	devs, err := devices.Load(filepath.Join(dir, devices.File))
	if errors.Is(err, fs.ErrNotExist) {
		devs, err = nil, nil // nothing paired yet
	}
	if err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	st, running, err := status.Read(dir, h.clock.Now())
	running = running && err == nil
	type device struct {
		Name        string    `json:"name"`
		Fingerprint string    `json:"fingerprint"`
		Added       time.Time `json:"added"`
		Connected   bool      `json:"connected"`
		// Networks are the names of the networks it was paired with on this
		// page, for Change the Wi-Fi networks to fill in: never a password.
		Networks []string `json:"networks,omitempty"`
	}
	list := []device{}
	h.pairMu.Lock()
	for _, d := range devs {
		connected := running && slices.Contains(st.Connected, d.Name)
		list = append(list, device{d.Name, d.Fingerprint(), d.Added, connected, h.netNames[d.Name]})
	}
	h.pairMu.Unlock()
	writeJSON(w, struct {
		Devices []device `json:"devices"`
		Running bool     `json:"running"` // whether a bridge is serving, so connected means anything
		Serve   string   `json:"serve"`   // the exact command that serves this bridge, for when none is
	}{list, running, h.serveCommand()})
}

// deviceRevoke is POST /api/devices/revoke.
func (h *Handler) deviceRevoke(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if h.opts.Home == "" {
		jsonError(w, http.StatusConflict, "the setup page wasn't given the bridge's directory")
		return
	}
	path := filepath.Join(config.Dir(h.opts.Home), devices.File)
	devs, err := devices.Load(path)
	if err == nil && !slices.ContainsFunc(devs, func(d devices.Device) bool { return d.Name == req.Name }) {
		jsonError(w, http.StatusNotFound, fmt.Sprintf("No device named %q is paired.", req.Name))
		return
	}
	if err == nil {
		err = devices.Revoke(path, req.Name)
	}
	if err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}
	h.step(fmt.Sprintf("Revoked %s: the bridge no longer accepts its key. To use it again, pair it again.", req.Name))
	writeJSON(w, struct {
		Revoked string `json:"revoked"`
	}{req.Name})
}

// shippedFirmware is the version of the firmware the page would flash, ""
// with none.
func (h *Handler) shippedFirmware() string {
	fw, err := loadFirmware(h.firmware)
	if err != nil {
		return ""
	}
	return fw.version
}

// loginNoBrew is what to do when Homebrew isn't there to run the bridge.
const loginNoBrew = "Homebrew isn't installed, so the page can't run the bridge at login. " +
	"A bridge built from source runs at login with the launchd plist: docs/bridge.md §Run it at login."

// loginSet is POST /api/login.
func (h *Handler) loginSet(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On      *bool `json:"on"`
		Launchd bool  `json:"launchd"` // the person's consent to the launchd agent
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.On == nil {
		jsonError(w, http.StatusBadRequest, "The request isn't the JSON the page sends.")
		return
	}
	if exe := h.opts.Executable; exe != "" && !strings.Contains(exe, "/Cellar/"+loginFormula+"/") {
		h.loginLaunchd(w, *req.On, req.Launchd)
		return
	}
	var out []byte
	var err error
	if h.opts.RunBrew != nil {
		out, err = setLogin(*req.On, h.opts.RunBrew)
	} else {
		out, err = setLogin(*req.On, func(argv ...string) ([]byte, error) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			c := exec.CommandContext(ctx, argv[0], argv[1:]...)
			c.Env = append(os.Environ(), "HOMEBREW_NO_AUTO_UPDATE=1", "HOMEBREW_NO_ENV_HINTS=1")
			return c.CombinedOutput()
		})
	}
	said := []string{} // brew's output, a line at a time, as text
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(plainText(l)); l != "" {
			said = append(said, l)
		}
	}
	switch {
	case missing(err):
		h.step(loginNoBrew)
		jsonError(w, http.StatusConflict, loginNoBrew)
		return
	case err != nil:
		msg := fmt.Sprintf("brew services couldn't %s herdr-pager (%v): %s", map[bool]string{true: "start", false: "stop"}[*req.On], err, strings.Join(said, " "))
		h.step(msg)
		jsonError(w, http.StatusUnprocessableEntity, msg)
		return
	}
	for _, l := range said {
		h.step(l)
	}
	writeJSON(w, struct {
		On     bool     `json:"on"`
		Output []string `json:"output"`
	}{*req.On, said})
}

// loginFormula is Homebrew's name for the bridge: a bridge whose path isn't
// in its Cellar wasn't installed by Homebrew.
const loginFormula = "herdr-pager"

// launchdLabel is the launchd agent's label (contrib/launchd).
const launchdLabel = "dev.herdr-pager.bridge"

// launchdPlist is contrib/launchd/dev.herdr-pager.bridge.plist, filled in:
// the program, then the home folder three times, each XML-escaped.
const launchdPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<!-- pager-bridge serve at login: a launchd user agent, written by the setup page (contrib/launchd). -->
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + launchdLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>%[1]s</string>
		<string>serve</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>HOME</key>
		<string>%[2]s</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	<string>%[2]s/Library/Logs/herdr-pager/bridge.log</string>
	<key>StandardErrorPath</key>
	<string>%[2]s/Library/Logs/herdr-pager/bridge.log</string>
</dict>
</plist>
`

// loginLaunchd is POST /api/login for a bridge Homebrew didn't install (the
// owner's checkpoint 7.2): on, with the person's consent, it writes the
// launchd agent that runs this executable at login, and answers the command
// that starts it now, or, when an agent was there, the two that reload it;
// without consent, it answers what it would write, and which bridge an agent
// already there runs (the owner's second recheck: rc.3's agent, while rc.4's
// page said Run at login was on); off, it removes the agent, and answers the
// command that stops it now. The page runs no launchctl command: the person
// does, in Terminal.
func (h *Handler) loginLaunchd(w http.ResponseWriter, on, consent bool) {
	exe := h.opts.Executable
	plist := filepath.Join(h.opts.Home, "Library", "LaunchAgents", launchdLabel+".plist")
	tilde := "~/Library/LaunchAgents/" + launchdLabel + ".plist"
	_, err := os.Stat(plist)
	existed := err == nil
	current, err := doctor.LaunchdProgram(plist)
	other := existed && (err != nil || !doctor.SameProgram(current, exe))
	answer := func(reload bool, cmds ...string) {
		writeJSON(w, struct {
			On       bool     `json:"on"`
			Plist    string   `json:"plist"`
			Commands []string `json:"commands"`
			Reload   bool     `json:"reload"` // the commands stop the job launchd runs, and start the new one
		}{on, plist, cmds, reload})
	}
	switch {
	case !on:
		if err := os.Remove(plist); err != nil && !errors.Is(err, fs.ErrNotExist) {
			jsonError(w, http.StatusConflict, err.Error())
			return
		}
		h.step("Removed the launchd agent " + launchdLabel + ": the bridge won't start at login. Stop the one running now with: launchctl bootout gui/$(id -u)/" + launchdLabel)
		answer(false, "launchctl bootout gui/$(id -u)/"+launchdLabel)
	case !consent:
		msg := "This pager-bridge wasn't installed with Homebrew, so brew services can't run it. A launchd agent can run it at login instead: " +
			plist + ", running " + exe + " serve. It runs this copy, where it is: move or delete it, and the agent fails."
		runsNow := "" // the bridge an agent already there runs, when it's another
		if other {
			runsNow = current
			runs := "a bridge the page can't read from it"
			if current != "" {
				runs = "another pager-bridge: " + current + " serve"
			}
			msg = "The launchd agent " + launchdLabel + " runs " + runs + ". Switch it to this one, " + exe +
				"? The page rewrites " + plist + " to run this copy, where it is, and gives the commands that reload it."
		}
		type offer struct {
			Plist   string `json:"plist"`
			Program string `json:"program"`           // the bridge it would run: this one
			Current string `json:"current,omitempty"` // runsNow
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(struct {
			Error   string `json:"error"`
			Launchd offer  `json:"launchd"`
		}{msg, offer{plist, exe, runsNow}})
	default:
		esc := func(s string) string {
			var b strings.Builder
			xml.EscapeText(&b, []byte(s))
			return b.String()
		}
		err := os.MkdirAll(filepath.Dir(plist), 0o755)
		if err == nil {
			err = os.MkdirAll(filepath.Join(h.opts.Home, "Library", "Logs", "herdr-pager"), 0o755)
		}
		if err == nil {
			err = os.WriteFile(plist, []byte(fmt.Sprintf(launchdPlist, esc(exe), esc(h.opts.Home))), 0o644)
		}
		if err != nil {
			jsonError(w, http.StatusConflict, "writing the launchd agent: "+err.Error())
			return
		}
		start := "launchctl bootstrap gui/$(id -u) " + tilde
		if !existed {
			h.step("Wrote the launchd agent " + plist + ": it runs " + exe + " serve at login. Start it now with: " + start)
			answer(false, start)
			return
		}
		stop := "launchctl bootout gui/$(id -u)/" + launchdLabel
		h.step("Rewrote the launchd agent " + plist + ": it runs " + exe + " serve at login. launchd still runs the bridge it ran before until it's reloaded: " + stop + ", then " + start)
		answer(true, stop, start)
	}
}

// serveCommand is the exact command that serves this bridge, for a person to
// run in Terminal: this executable, quoted for a shell, and serve.
func (h *Handler) serveCommand() string {
	exe := h.opts.Executable
	switch {
	case exe == "":
		return "pager-bridge serve"
	case strings.Trim(exe, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%_+=:,./-") == "":
		return exe + " serve"
	}
	return "'" + strings.ReplaceAll(exe, "'", `'\''`) + "' serve"
}

// setLogin runs the bridge at login, or stops it, by brew services: by brew
// on PATH, else where Homebrew puts it on Apple Silicon. Every command is
// written out here, its words fixed, so the repository's check of process
// launches (internal/repocheck, parent 13.3) sees each one.
func setLogin(on bool, brew func(argv ...string) ([]byte, error)) ([]byte, error) {
	if on {
		out, err := brew("brew", "services", "start", "herdr-pager")
		if missing(err) {
			out, err = brew("/opt/homebrew/bin/brew", "services", "start", "herdr-pager")
		}
		return out, err
	}
	out, err := brew("brew", "services", "stop", "herdr-pager")
	if missing(err) {
		out, err = brew("/opt/homebrew/bin/brew", "services", "stop", "herdr-pager")
	}
	return out, err
}

// missing is whether err says the command isn't there.
func missing(err error) bool {
	return errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist)
}

// doctorReport is GET /api/doctor: the bridge's checks, as `pager-bridge
// doctor --json` gives them. The step channel shows them as `pager-bridge
// doctor` prints them (design §The setup page, Live output).
func (h *Handler) doctorReport(w http.ResponseWriter, r *http.Request) {
	var report doctor.Report
	if h.opts.Doctor != nil {
		report = h.opts.Doctor()
	} else {
		env, err := doctor.System()
		if err != nil {
			h.step("The bridge's checks failed: " + err.Error())
			jsonError(w, http.StatusInternalServerError, "the bridge's checks: "+err.Error())
			return
		}
		report = doctor.Run(env)
	}
	report.WriteText(h.stepWriter())
	writeJSON(w, report)
}
