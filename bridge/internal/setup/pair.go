package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/config"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/provision"
)

// Pairing through the page (requirements 2.1, 2.6, 2.9; design §The setup
// page, Pairing; ADR 0011), as `pager-bridge provision` pairs, in two steps:
//
//   - POST /api/pair {port, name, host}: opens the pager's port, waits for
//     its hello, sends it the prov_offer (the bridge's key and the Mac's
//     name, nothing secret), and answers both full fingerprints, the
//     bridge's and the pager's. The pager shows the same two, and the
//     person confirms there with BOOT.
//   - POST /api/pair/confirm {fingerprint, wifi}: the person's
//     confirmation on the page ("They match: send the Wi-Fi networks"),
//     with the pager's fingerprint as the page showed it, and the networks
//     with their passwords. Once the pager has reported BOOT too (the
//     request waits for it), and only then, the bridge sends the prov_set;
//     it answers the pager's result.
//
// POST /api/pair/cancel ends a pairing before its networks go, and GET
// /api/ports lists the ports the page may pair through. Every step's output
// goes to the step channel. A password lives only in its confirm request's
// memory: never a file, a log, an event, a response or a URL. The pager's
// own limits end a pairing nobody confirms: 30 s for BOOT, then 30 s for
// the networks (provision's waits).

// The most a request body may hold: four networks fit many times over.
const maxBody = 16 << 10

// A pairing is the one pairing under way.
type pairing struct {
	state  pairState
	cancel context.CancelFunc // ends the wait for the hello (offering)
	p      *provision.Pairing // from offered on
	ended  chan struct{}      // closed when it's over and its port is closed
	port   string             // the pager's port, which POST /api/pair/join reads next
}

type pairState int

const (
	offering   pairState = iota // waiting for the pager's hello
	offered                     // the prov_offer sent, both fingerprints shown, waiting for the confirm
	confirming                  // confirmed on the page: waiting for the pager's BOOT, then its result
)

// endPairing ends pr, whose port is closed by now; the caller holds pairMu.
func (h *Handler) endPairing(pr *pairing) {
	if h.pair == pr {
		h.pair = nil
		close(pr.ended)
	}
}

// pairingEnded waits for the pairing under way, if any, to end. Serve calls
// it once life has ended, which ends every wait a pairing makes.
func (h *Handler) pairingEnded() {
	h.pairMu.Lock()
	pr := h.pair
	h.pairMu.Unlock()
	if pr != nil {
		<-pr.ended
	}
}

// SerialPorts are the Mac's USB serial ports: the pager's is one.
func SerialPorts() ([]string, error) { return filepath.Glob("/dev/cu.usbmodem*") }

func (h *Handler) ports() ([]string, error) {
	if h.opts.Ports != nil {
		return h.opts.Ports()
	}
	return SerialPorts()
}

// portList is GET /api/ports: the ports the page may pair through. With
// more than one, the page asks which is the pager's.
func (h *Handler) portList(w http.ResponseWriter, r *http.Request) {
	ports, err := h.ports()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "listing the serial ports: "+err.Error())
		return
	}
	writeJSON(w, struct {
		Ports []string `json:"ports"`
	}{append([]string{}, ports...)})
}

// pairStart is POST /api/pair.
func (h *Handler) pairStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Port string `json:"port"`
		Name string `json:"name"`
		Host string `json:"host"` // "" for the Mac's <LocalHostName>.local
		// Replace is the fingerprint of the key that devices.toml holds
		// under Name, which the person agreed to replace: that key is revoked
		// before the pairing starts, if it's still Name's.
		Replace string `json:"replace"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	h.stopJoin() // it may hold the port this pairing opens
	if req.Replace != "" && !h.replaceDevice(w, req.Name, req.Replace) {
		return
	}
	ports, err := h.ports()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "listing the serial ports: "+err.Error())
		return
	}
	if !slices.Contains(ports, req.Port) {
		jsonError(w, http.StatusBadRequest, fmt.Sprintf("%q isn't one of this Mac's USB serial ports. Plug the pager in, and choose its port.", req.Port))
		return
	}
	bridge, err := h.bridgeSide()
	if err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return
	}

	ctx, cancel := context.WithCancel(r.Context()) // a tab that goes ends the wait
	defer cancel()
	defer context.AfterFunc(h.life, cancel)()
	pr := &pairing{state: offering, cancel: cancel, ended: make(chan struct{}), port: req.Port}
	h.pairMu.Lock()
	if h.pair != nil {
		h.pairMu.Unlock()
		jsonError(w, http.StatusConflict, "A pairing is under way already. Cancel it first.")
		return
	}
	h.pair = pr
	h.pairMu.Unlock()

	p, err := provision.Offer(ctx, provision.Options{
		Port:          req.Port,
		Name:          req.Name,
		Host:          req.Host,
		BridgePort:    bridge.port,
		HostKey:       bridge.hostKey,
		Devices:       bridge.devices,
		Out:           h.stepWriter(),
		Log:           h.pagerLine,
		LocalHostName: h.opts.LocalHostName,
		KnownNetworks: h.opts.KnownNetworks,
		After:         h.clock.After,
		Now:           h.clock.Now,
	})
	h.pairMu.Lock()
	if err != nil {
		h.endPairing(pr)
		h.pairMu.Unlock()
		var bad *provision.SettingsError
		var taken *provision.NameTakenError
		var noHello *provision.NoHelloError
		switch {
		case errors.As(err, &bad):
			jsonError(w, http.StatusBadRequest, err.Error())
		case errors.As(err, &taken):
			msg := fmt.Sprintf("%s is already paired, with another key (%s): another pager, or this one before it was erased. Replace it, which unpairs that key, or choose another name. Nothing was sent to the pager.",
				taken.Name, taken.Fingerprint)
			h.step(msg)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			json.NewEncoder(w).Encode(struct {
				Error string `json:"error"`
				Taken struct {
					Name        string `json:"name"`
					Fingerprint string `json:"fingerprint"`
				} `json:"taken"`
			}{Error: msg, Taken: struct {
				Name        string `json:"name"`
				Fingerprint string `json:"fingerprint"`
			}{taken.Name, taken.Fingerprint}})
		case errors.As(err, &noHello):
			// The CLI's words name provision and --port (the owner's last
			// recheck of 7.2, open-items O-96).
			msg := "No hello from the pager in 2 minutes. Nothing was sent to the pager, and devices.toml is as it was. " +
				"Put it in pairing mode: a pager paired before needs Pair again, in its menu's SETTINGS, until it shows PAIR VIA USB. Then Start again. " +
				"If it showed PAIR VIA USB all along, check the port and the cable."
			h.step("Pairing failed: " + msg)
			jsonError(w, http.StatusUnprocessableEntity, msg)
		case ctx.Err() != nil:
			h.step("Pairing cancelled: nothing was sent to the pager.")
			jsonError(w, http.StatusConflict, "Pairing was cancelled.")
		default:
			h.step("Pairing failed: " + err.Error())
			jsonError(w, http.StatusUnprocessableEntity, err.Error())
		}
		return
	}
	pr.p, pr.state = p, offered
	h.pairMu.Unlock()
	go h.awaitConfirm(pr)

	type key struct {
		Fingerprint string `json:"fingerprint"`
		Type        string `json:"type"`
		FW          string `json:"fw,omitempty"`
	}
	type firmware struct {
		Pager   string `json:"pager"`   // the pager's fw, from its hello
		Shipped string `json:"shipped"` // the version the page would flash; "" with none
		Update  bool   `json:"update"`  // whether they differ: Manage offers Update
	}
	shipped := h.shippedFirmware()
	writeJSON(w, struct {
		Name     string   `json:"name"`
		Port     string   `json:"port"`
		Host     string   `json:"host"`
		HostName string   `json:"host_name"`
		Pager    key      `json:"pager"`
		Bridge   key      `json:"bridge"`
		Firmware firmware `json:"firmware"`
	}{
		Name: req.Name, Port: req.Port, Host: p.Host(), HostName: p.HostName(),
		Pager:    key{ssh.FingerprintSHA256(p.DeviceKey()), p.DeviceKey().Type(), p.FW()},
		Bridge:   key{ssh.FingerprintSHA256(p.HostKey()), p.HostKey().Type(), ""},
		Firmware: firmware{p.FW(), shipped, shipped != "" && shipped != p.FW()},
	})
}

// replaceDevice revokes name from devices.toml if it's still paired with the
// key whose fingerprint is fp, the one the page showed when it offered to
// replace it. It answers the request, and returns false, if it isn't.
func (h *Handler) replaceDevice(w http.ResponseWriter, name, fp string) bool {
	if h.opts.Home == "" {
		jsonError(w, http.StatusConflict, "the setup page wasn't given the bridge's directory")
		return false
	}
	path := filepath.Join(config.Dir(h.opts.Home), devices.File)
	devs, err := devices.Load(path)
	if err == nil && !slices.ContainsFunc(devs, func(d devices.Device) bool { return d.Name == name && d.Fingerprint() == fp }) {
		jsonError(w, http.StatusConflict, fmt.Sprintf("%s isn't paired with %s any more, so nothing was replaced. Start pairing again.", name, fp))
		return false
	}
	if err == nil {
		err = devices.Revoke(path, name)
	}
	if err != nil {
		jsonError(w, http.StatusConflict, err.Error())
		return false
	}
	h.step(fmt.Sprintf("Replaced %s: the bridge no longer accepts its old key, %s. Pairing this pager as %s.", name, fp, name))
	return true
}

// awaitConfirm says on the step channel when the pager confirms with BOOT,
// and ends pr if it's still waiting for its confirm when the pager or
// provision's waits end it, or when the page stops.
func (h *Handler) awaitConfirm(pr *pairing) {
	booted := pr.p.PagerConfirmed()
	var why string
	for why == "" {
		select {
		case <-pr.ended:
			return
		case <-booted:
			booted = nil
			h.step("The pager confirmed with BOOT. If both fingerprints match, send the Wi-Fi networks now: it waits 30 s for them.")
		case <-pr.p.Over():
			why = fmt.Sprintf("The pairing has ended: %v. No Wi-Fi password was sent to the pager. Start pairing again.", pr.p.Err())
		case <-h.life.Done():
			why = "The setup page stopped, so the pairing has ended: no Wi-Fi password was sent to the pager."
		}
	}
	h.pairMu.Lock()
	defer h.pairMu.Unlock()
	if h.pair == pr && pr.state == offered {
		pr.p.Close()
		h.endPairing(pr)
		h.step(why)
	}
}

// pairConfirm is POST /api/pair/confirm.
func (h *Handler) pairConfirm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Fingerprint string `json:"fingerprint"` // the pager's, as the page showed it
		WiFi        []struct {
			SSID string `json:"ssid"`
			Pass string `json:"pass"`
		} `json:"wifi"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	nets := make([]provision.Network, len(req.WiFi))
	for i, n := range req.WiFi {
		nets[i] = provision.Network{SSID: n.SSID, Pass: n.Pass}
	}

	h.pairMu.Lock()
	pr := h.pair
	switch {
	case pr == nil || pr.state != offered:
		h.pairMu.Unlock()
		jsonError(w, http.StatusConflict, "No pairing is waiting to be confirmed. Start pairing again.")
		return
	case req.Fingerprint != ssh.FingerprintSHA256(pr.p.DeviceKey()):
		h.pairMu.Unlock()
		jsonError(w, http.StatusConflict, "That isn't the fingerprint of the pager this pairing has. Compare them again.")
		return
	}
	if err := pr.p.Check(nets); err != nil {
		h.pairMu.Unlock()
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	pr.state = confirming
	h.pairMu.Unlock()

	// The page's life, not the request's: a tab that closes now mustn't
	// leave a pager that saved its settings unknown to devices.toml.
	err := pr.p.Set(h.life, nets)
	pr.p.Close()
	h.pairMu.Lock()
	h.endPairing(pr)
	if err == nil { // for POST /api/pair/join, and Change the Wi-Fi networks
		h.last = lastPairing{pr.port, pr.p.Name()}
		ssids := make([]string, len(nets))
		for i, n := range nets {
			ssids[i] = n.SSID
		}
		h.netNames[pr.p.Name()] = ssids
	}
	h.pairMu.Unlock()
	if err != nil {
		h.step("Pairing failed: " + err.Error())
		jsonError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, struct {
		Paired      string `json:"paired"`
		Fingerprint string `json:"fingerprint"`
	}{pr.p.Name(), ssh.FingerprintSHA256(pr.p.DeviceKey())})
}

// pairCancel is POST /api/pair/cancel: it ends the pairing under way, if
// its networks haven't gone yet.
func (h *Handler) pairCancel(w http.ResponseWriter, r *http.Request) {
	h.pairMu.Lock()
	pr := h.pair
	switch {
	case pr == nil:
		h.pairMu.Unlock()
		writeJSON(w, struct {
			Cancelled bool `json:"cancelled"`
		}{false})
		return
	case pr.state == confirming:
		h.pairMu.Unlock()
		jsonError(w, http.StatusConflict, "The networks go to the pager as soon as it confirms with BOOT: press PWR on it to cancel.")
		return
	case pr.state == offering:
		pr.cancel() // pairStart ends it, and says so
		h.pairMu.Unlock()
		<-pr.ended
	default:
		pr.p.Close()
		h.endPairing(pr)
		h.pairMu.Unlock()
		h.step("Pairing cancelled: no Wi-Fi password was sent to the pager. It stops waiting within 30 s, or press PWR on it now.")
	}
	writeJSON(w, struct {
		Cancelled bool `json:"cancelled"`
	}{true})
}

// A bridgeSide is what a pairing needs from the bridge's directory.
type bridgeSide struct {
	hostKey ssh.PublicKey
	port    int    // the port it listens on: config.toml's listen
	devices string // devices.toml's path
}

func (h *Handler) bridgeSide() (bridgeSide, error) {
	if h.opts.Home == "" {
		return bridgeSide{}, errors.New("the setup page wasn't given the bridge's directory")
	}
	getenv := h.opts.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	dir := config.Dir(h.opts.Home)
	cfg, err := config.Load(filepath.Join(dir, config.File), config.Env{Home: h.opts.Home, Getenv: getenv})
	if err != nil {
		return bridgeSide{}, noKeys(err)
	}
	key, err := config.HostKey(dir)
	if err != nil {
		return bridgeSide{}, noKeys(err)
	}
	_, p, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return bridgeSide{}, err
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		return bridgeSide{}, fmt.Errorf("config.toml: listen: port %q isn't a number", p)
	}
	return bridgeSide{key, port, filepath.Join(dir, devices.File)}, nil
}

// noKeys says what to do about a bridge file that isn't there.
func noKeys(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: the bridge has no keys yet. Make them in the first step, Check the Mac, or run `pager-bridge init`", err)
	}
	return err
}

// readJSON decodes the request's body into v, or answers 400. The answer
// never quotes the body, which may hold a password.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if dec.Decode(v) != nil || dec.More() {
		jsonError(w, http.StatusBadRequest, "The request isn't the JSON the page sends.")
		return false
	}
	return true
}

// jsonError answers {"error": msg} with status.
func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSONStatus(w, status, struct {
		Error string `json:"error"`
	}{msg})
}
