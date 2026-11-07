package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"text/tabwriter"
	"time"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/config"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/status"
)

var devicesCmd = command{"devices", []string{
	"devices list", "List the paired devices: name, key type, full fingerprint, date, connected.",
	"devices revoke <name>", "Remove a device; a running bridge closes its session within 2 s.",
}, runDevices}

// runDevices is `devices list`, each device with its full fingerprint and
// whether it's connected (from a fresh status.json), and `devices revoke
// <name>`, which rewrites devices.toml atomically (parent 12.1–12.3; lane cli,
// tasks 2.4, 2.5).
func runDevices(c *cli, args []string) int {
	switch {
	case len(args) == 1 && args[0] == "list":
		return devicesList(c)
	case len(args) == 2 && args[0] == "revoke":
		return devicesRevoke(c, args[1])
	}
	fmt.Fprintf(c.stderr, "pager-bridge devices: use `devices list` or `devices revoke <name>`\n")
	return 2
}

// devicesList prints the paired devices as a table. A device is connected
// only if a running bridge says so in status.json (design D2-12).
func devicesList(c *cli) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(c.stderr, "pager-bridge devices list: %v\n", err)
		return 1
	}
	dir := config.Dir(home)
	devs, err := devices.Load(filepath.Join(dir, devices.File))
	if err != nil {
		fmt.Fprintf(c.stderr, "pager-bridge devices list: %v\n", err)
		return 1
	}
	if len(devs) == 0 {
		fmt.Fprintf(c.stdout, "No paired devices. Pair one with `pager-bridge provision`.\n")
		return 0
	}
	st, running, err := status.Read(dir, time.Now())
	if err != nil {
		fmt.Fprintf(c.stderr, "pager-bridge devices list: %v\n", err)
		return 1
	}
	w := tabwriter.NewWriter(c.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tTYPE\tFINGERPRINT\tADDED\tCONNECTED")
	for _, d := range devs {
		connected := "no"
		if slices.Contains(st.Connected, d.Name) {
			connected = "yes"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", d.Name, d.KeyType(), d.Fingerprint(), d.Added.UTC().Format(time.RFC3339), connected)
	}
	w.Flush()
	if !running {
		fmt.Fprintf(c.stdout, "No bridge is running, so none is connected.\n")
	}
	return 0
}

// devicesRevoke removes the device named name from devices.toml. A running
// bridge sees the change within 500 ms and closes its session (phase 2
// R5.6).
func devicesRevoke(c *cli, name string) int {
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(c.stderr, "pager-bridge devices revoke: %v\n", err)
		return 1
	}
	path := filepath.Join(config.Dir(home), devices.File)
	devs, err := devices.Load(path)
	if err == nil {
		err = devices.Revoke(path, name)
	}
	if err != nil {
		fmt.Fprintf(c.stderr, "pager-bridge devices revoke: %v\n", err)
		return 1
	}
	for _, d := range devs {
		if d.Name == name {
			fmt.Fprintf(c.stdout, "Revoked %s (%s %s). A running bridge closes its session within 2 s.\n",
				d.Name, d.KeyType(), d.Fingerprint())
		}
	}
	return 0
}
