package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/config"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/status"
)

var statusCmd = command{"status", []string{
	"status [--json]", "Show whether a bridge is listening, herdr's socket, the devices and which are connected.",
}, runStatus}

// A statusReport is what status prints. While a bridge runs it's status.json,
// which serve keeps; else it's devices.toml's devices and config.toml's
// socket, none connected, and herdr's state unknown, since only a running
// bridge talks to herdr.
type statusReport struct {
	Running   bool     `json:"running"`
	Listening string   `json:"listening"` // where the bridge listens, while it runs
	Herdr     string   `json:"herdr"`     // ok, offline, or unknown while none runs
	Socket    string   `json:"socket"`    // herdr's socket
	Devices   []string `json:"devices"`   // the paired devices' names
	Connected []string `json:"connected"` // the names with a session open
	// DevicesError is why devices.toml won't load, while a running bridge
	// can't load it: then it pairs no device (open-items O-51).
	DevicesError string `json:"devices_error,omitempty"`
}

// runStatus prints, from status.json, whether a bridge is listening, herdr's
// socket state, the paired devices and which are connected; --json prints the
// same as JSON (parent 11.2, design D2-12; lane e2e, task 4.25).
func runStatus(c *cli, args []string) int {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	asJSON := flags.Bool("json", false, "print the same as JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintln(c.stderr, "pager-bridge status: takes no arguments")
		return 2
	}
	r, err := readStatusReport()
	if err != nil {
		fmt.Fprintf(c.stderr, "pager-bridge status: %v\n", err)
		return 1
	}
	if *asJSON {
		data, err := json.Marshal(r)
		if err != nil {
			fmt.Fprintf(c.stderr, "pager-bridge status: %v\n", err)
			return 1
		}
		fmt.Fprintf(c.stdout, "%s\n", data)
		return 0
	}
	w := tabwriter.NewWriter(c.stdout, 0, 0, 2, ' ', 0)
	if r.Running {
		fmt.Fprintf(w, "Bridge:\tlistening on %s\n", r.Listening)
		fmt.Fprintf(w, "herdr:\t%s, at %s\n", r.Herdr, r.Socket)
	} else {
		fmt.Fprintf(w, "Bridge:\tnot running\n")
		fmt.Fprintf(w, "herdr:\tunknown while no bridge runs (its socket: %s)\n", r.Socket)
	}
	switch {
	case r.DevicesError != "":
		fmt.Fprintf(w, "Devices:\tdevices.toml won't load (%s); no device can connect until it's fixed\n", r.DevicesError)
	case len(r.Devices) == 0:
		fmt.Fprintf(w, "Devices:\tnone paired; pair one with `pager-bridge provision`\n")
	default:
		fmt.Fprintf(w, "Devices:\t%d paired, %d connected\n", len(r.Devices), len(r.Connected))
	}
	w.Flush()
	w = tabwriter.NewWriter(c.stdout, 0, 0, 2, ' ', 0)
	for _, name := range r.Devices {
		state := "not connected"
		if slices.Contains(r.Connected, name) {
			state = "connected"
		}
		fmt.Fprintf(w, "  %s\t%s\n", name, state)
	}
	w.Flush()
	return 0
}

// readStatusReport reads the bridge's state: status.json, if a bridge is
// running; else devices.toml and config.toml.
func readStatusReport() (statusReport, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return statusReport{}, err
	}
	dir := config.Dir(home)
	st, running, err := status.Read(dir, time.Now())
	if err != nil {
		return statusReport{}, err
	}
	if running {
		return statusReport{Running: true, Listening: st.Listening, Herdr: st.Herdr, Socket: st.Socket,
			Devices: st.Devices, Connected: st.Connected, DevicesError: st.DevicesError}, nil
	}
	cfg, err := config.Load(filepath.Join(dir, config.File), config.Env{Home: home, Getenv: os.Getenv})
	if errors.Is(err, fs.ErrNotExist) {
		err = fmt.Errorf("%w; run pager-bridge init first", err)
	}
	if err != nil {
		return statusReport{}, err
	}
	devs, err := devices.Load(filepath.Join(dir, devices.File))
	if err != nil {
		return statusReport{}, err
	}
	return statusReport{Herdr: "unknown", Socket: cfg.HerdrSocket, Devices: pairedNames(devs), Connected: []string{}}, nil
}

// pairedNames is each device's name, in devices.toml's order.
func pairedNames(devs []devices.Device) []string {
	names := make([]string, 0, len(devs))
	for _, d := range devs {
		names = append(names, d.Name)
	}
	return names
}
