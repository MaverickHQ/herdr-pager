package setup

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/clock"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/doctor"
)

// Options are what the page needs from the bridge.
type Options struct {
	Clock clock.Clock // nil for the real one
	// Firmware is the folder of images the page flashes: a release's
	// share/herdr-pager/firmware (DefaultFirmware), or a source build's
	// ESP-IDF build directory (`pager-bridge setup --firmware <dir>`).
	Firmware string

	// Pairing (pair.go). Home is the user's home: the bridge's directory
	// (config.Dir) holds its host key, config.toml and devices.toml, read as
	// each pairing starts, so a key made since the page started is the one
	// the pager is given.
	Home   string
	Getenv func(string) string // for config.toml's defaults; nil reads no variable
	// Ports lists the serial ports the page may pair through; nil for the
	// Mac's USB serial ports (SerialPorts).
	Ports func() ([]string, error)
	// LocalHostName and KnownNetworks are provision's: the Mac's name, and
	// the Wi-Fi networks it has saved, for the near-miss warning.
	LocalHostName func() (string, error)
	KnownNetworks func() []string
	// Manage (manage.go). RunBrew runs one of brew services' fixed commands
	// and returns its output; nil for the real brew. Doctor gives the
	// bridge's checks; nil for doctor.Run on this Mac.
	RunBrew func(argv ...string) ([]byte, error)
	Doctor  func() doctor.Report
	// Executable is the bridge's own path, symlinks resolved: what serves
	// this bridge, and what the launchd agent runs at login when Homebrew
	// didn't install it (the owner's checkpoint 7.2). "" leaves Run at login
	// to brew services.
	Executable string
	// BridgeLogs are the files the bridge's log may be in (BridgeLogs): Serve
	// shows each line appended to one on the bridge channel.
	BridgeLogs []string
	// Log gets each line the page shows on its step channel, for the
	// terminal `pager-bridge setup` runs in; nil for nowhere. It never gets a
	// password.
	Log io.Writer
}

// A Handler is the setup page: its guard, then its routes. It's the seam
// the page's tests drive (S5P-4), with httptest.
type Handler struct {
	token    string
	clock    clock.Clock
	firmware string // Options.Firmware
	opts     Options
	mux      *http.ServeMux
	tabs     streams
	hub      hub

	log   io.Writer // Options.Log
	logMu sync.Mutex

	// life ends when Serve stops: a pairing waiting on the pager ends with it.
	life     context.Context
	stopLife context.CancelFunc

	pairMu sync.Mutex
	pair   *pairing // the pairing under way, if any: one at a time
	// After a pairing (join.go): the last that succeeded, the names of the
	// networks each device was paired with on this page (never their
	// passwords), and the join watch reading its port, if any.
	last     lastPairing
	netNames map[string][]string
	joinW    *joinWatch
}

// NewHandler is the setup page for opts, with a new token.
func NewHandler(opts Options) (*Handler, error) {
	token, err := newToken()
	if err != nil {
		return nil, err
	}
	if opts.Clock == nil {
		opts.Clock = clock.Real{}
	}
	h := &Handler{token: token, clock: opts.Clock, firmware: opts.Firmware, opts: opts, log: opts.Log, mux: http.NewServeMux(), netNames: map[string][]string{}}
	h.life, h.stopLife = context.WithCancel(context.Background())
	h.tabs.init()
	h.mux.HandleFunc("GET /events", h.events)
	h.mux.HandleFunc("GET /api/firmware", h.firmwareInfo)
	h.mux.HandleFunc("GET /firmware/{path...}", h.firmwareFile)
	h.mux.HandleFunc("GET /api/ports", h.portList)
	h.mux.HandleFunc("POST /api/pair", h.pairStart)
	h.mux.HandleFunc("POST /api/pair/confirm", h.pairConfirm)
	h.mux.HandleFunc("POST /api/pair/cancel", h.pairCancel)
	h.mux.HandleFunc("POST /api/pair/join", h.pairJoin)
	h.mux.HandleFunc("GET /api/devices", h.devicesList)
	h.mux.HandleFunc("POST /api/devices/revoke", h.deviceRevoke)
	h.mux.HandleFunc("POST /api/login", h.loginSet)
	h.mux.HandleFunc("GET /api/doctor", h.doctorReport)
	h.mux.HandleFunc("POST /api/keys", h.keysMake)
	h.mux.HandleFunc("POST /api/wifi/check", h.wifiCheck)
	h.mux.Handle("GET /", pageFiles())
	return h, nil
}

// Token is the page's token, for the URL `pager-bridge setup` opens.
func (h *Handler) Token() string { return h.token }

// ServeHTTP sets the page's headers on every response, refuses what the
// guard refuses, and serves the rest.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, kv := range pageHeaders {
		w.Header().Set(kv[0], kv[1])
	}
	if !h.guard(w, r) {
		return
	}
	h.mux.ServeHTTP(w, r)
}

// Listen is the page's listener: 127.0.0.1 only, on a port the system
// chooses (requirement 2.5).
func Listen() (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }
