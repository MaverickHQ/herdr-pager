package setup

import (
	"net/http"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/config"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/provision"
)

// What the page's steps need beyond pairing and Manage (requirement 2.1):
//
//   - POST /api/keys: the bridge's keys, made if missing, as `pager-bridge
//     init` makes them (config.Init), and kept if not. It answers the host
//     key's full fingerprint.
//   - POST /api/wifi/check {ssids}: the Wi-Fi networks the Mac has saved,
//     and each name given that nearly matches one of them
//     (provision.NearMisses), so the Wi-Fi step warns before pairing. Names
//     only: a password never comes here.

// keysMake is POST /api/keys.
func (h *Handler) keysMake(w http.ResponseWriter, r *http.Request) {
	if h.opts.Home == "" {
		jsonError(w, http.StatusConflict, "the setup page wasn't given the bridge's directory")
		return
	}
	key, err := config.Init(config.Dir(h.opts.Home))
	if err != nil {
		h.step("Making the bridge's keys failed: " + err.Error())
		jsonError(w, http.StatusConflict, "making the bridge's keys: "+err.Error())
		return
	}
	fp := ssh.FingerprintSHA256(key)
	h.step("The bridge's keys are in " + config.Dir(h.opts.Home) + ": its host key is " + fp + " (" + key.Type() + ").")
	writeJSON(w, struct {
		Fingerprint string `json:"fingerprint"`
		Type        string `json:"type"`
	}{fp, key.Type()})
}

// wifiCheck is POST /api/wifi/check.
func (h *Handler) wifiCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SSIDs []string `json:"ssids"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	known := []string{}
	if h.opts.KnownNetworks != nil {
		known = append(known, h.opts.KnownNetworks()...)
	}
	type nearMiss struct {
		Given string `json:"given"`
		Known string `json:"known"`
	}
	misses := []nearMiss{}
	for _, m := range provision.NearMisses(req.SSIDs, known) {
		misses = append(misses, nearMiss{m.Given, m.Known})
	}
	writeJSON(w, struct {
		Known      []string   `json:"known"`
		NearMisses []nearMiss `json:"near_misses"`
	}{known, misses})
}
