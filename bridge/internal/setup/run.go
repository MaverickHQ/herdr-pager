package setup

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/provision"
)

// Main is `pager-bridge setup [--firmware <dir>]` (setupCmd, in
// cmd/pager-bridge/main.go), whose runSetup calls it: its flags, then Run on
// this Mac until Ctrl-C. --firmware is the images to flash, such as a source
// build's ESP-IDF build directory; by default, the firmware installed beside
// this pager-bridge (DefaultFirmware). It returns the exit code: 0 once the
// page has stopped, 1 on an error, 2 on a bad flag or an argument.
func Main(stdout, stderr io.Writer, args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return mainWith(ctx, stdout, stderr, args, Run)
}

// mainWith is Main with its context and its Run given, for tests.
func mainWith(ctx context.Context, stdout, stderr io.Writer, args []string, run func(context.Context, RunOptions) error) int {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	firmware := flags.String("firmware", "", "the firmware to flash: a source build's ESP-IDF build directory, such as firmware/build (default: the one installed beside this pager-bridge)")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintln(stderr, "pager-bridge setup: takes no arguments")
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(stderr, "pager-bridge setup: %v\n", err)
		return 1
	}
	self := "" // this executable, symlinks resolved; "" leaves Run at login to brew services
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			self = real
		}
	}
	dir := *firmware
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(stderr, "pager-bridge setup: finding this pager-bridge, for its firmware: %v\n", err)
			return 1
		}
		dir = DefaultFirmware(exe)
	} else if dir, err = filepath.Abs(dir); err != nil {
		fmt.Fprintf(stderr, "pager-bridge setup: --firmware: %v\n", err)
		return 2
	}
	err = run(ctx, RunOptions{
		Options: Options{
			Firmware:      dir,
			Executable:    self,
			Home:          home,
			Getenv:        os.Getenv,
			LocalHostName: provision.LocalHostName,
			KnownNetworks: provision.KnownNetworks,
			BridgeLogs:    BridgeLogs(home),
			Log:           stdout, // the page's step lines, which never hold a password
		},
		Out: stdout,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pager-bridge setup: %v\n", err)
		return 1
	}
	return 0
}

// RunOptions are `pager-bridge setup`'s: the page's options, where to print,
// and the seam above the one launch of a browser.
type RunOptions struct {
	Options
	// Out gets the page's address and how long it lasts; nil for nowhere.
	Out io.Writer
	// Open opens the page in the browser, given the listener's port and the
	// page's token; nil for macOS's open (openPage). Tests replace it, so no
	// test opens a browser.
	Open func(port int, token string) error
}

// Run is `pager-bridge setup` (requirement 2.1): it serves the page on
// 127.0.0.1, prints its address for a browser that doesn't open, opens it,
// and serves it until ctx ends (Ctrl-C) or the page has had no tab for
// IdleAfter. It returns nil when the page stops either way.
func Run(ctx context.Context, o RunOptions) error {
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	h, err := NewHandler(o.Options)
	if err != nil {
		return err
	}
	ln, err := Listen()
	if err != nil {
		return fmt.Errorf("the setup page couldn't listen on 127.0.0.1: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	fmt.Fprintf(out, "Opening the setup page in your browser, at:\n\n  %s\n\n", pageURL(port, h.Token()))
	fmt.Fprintf(out, "If no browser opened, paste that address into Chrome or Edge on this Mac: only they can flash the pager.\n"+
		"The address works only while this runs. The page stops %d minutes after its last tab closes (or %d minutes from now, if no tab opens), or on Ctrl-C.\n\n",
		int(IdleAfter.Minutes()), int(IdleAfter.Minutes()))
	open := o.Open
	if open == nil {
		open = openPage
	}
	if err := open(port, h.Token()); err != nil {
		fmt.Fprintf(out, "Couldn't open a browser (%v): paste the address above into one.\n\n", err)
	}
	err = h.Serve(ctx, ln)
	fmt.Fprintln(out, "The setup page has stopped.")
	return err
}

// pageURL is the page's address on the listener's port, with its token: the
// one URL `pager-bridge setup` opens and prints. The guard trades the token
// for the page's cookie on the first request (guard.go).
func pageURL(port int, token string) string {
	return "http://127.0.0.1:" + strconv.Itoa(port) + "/?t=" + url.QueryEscape(token)
}

// openPage opens the page in the default browser with macOS's open: its own
// address, and nothing else. It's the bridge's one launch of open, in the
// form internal/repocheck allows (phase 5 design D5P-3 §Never; parent 13.3):
// no argument derived from an agent or a device.
func openPage(port int, token string) error {
	return exec.Command("/usr/bin/open", pageURL(port, token)).Run()
}
