package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/config"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/provision"
)

var provisionCmd = command{"provision", []string{
	"provision --port <serial> --name <name> --wifi-ssid <ssid> [--wifi-ssid <ssid>]... --wifi-pass-stdin [--host <host>] [--yes]",
	"Pair a pager over USB. Each network's password is one line of stdin, in --wifi-ssid order; never an argument. The pager shows both fingerprints and you press BOOT on it; then this asks, on the terminal, whether they match these, and sends the networks only on a yes. --yes skips that question (for unattended tests), never BOOT.",
}, runProvision}

// runProvision pairs a pager over its USB serial port, speaking the
// provisioning lines (testdata/provisioning/). It reads Wi-Fi passwords from
// stdin only and never prints them, sends them only once both fingerprints
// are confirmed, by BOOT on the pager and by the owner on /dev/tty (ADR
// 0011), and adds the device to devices.toml only after the device reports
// success (parent 2.3–2.10, phase 2 R4; lane cli, tasks 2.6–2.12).
func runProvision(c *cli, args []string) int {
	fl := flag.NewFlagSet("provision", flag.ContinueOnError)
	fl.SetOutput(c.stderr)
	var ssids ssidList
	port := fl.String("port", "", "the pager's serial port, such as /dev/cu.usbmodem1101")
	name := fl.String("name", "", "the pager's name, 1 to 24 of A-Z a-z 0-9 . _ -")
	fl.Var(&ssids, "wifi-ssid", "a Wi-Fi network, highest priority first; up to 4")
	passStdin := fl.Bool("wifi-pass-stdin", false, "read each network's password from stdin, one line each, in --wifi-ssid order (an empty line for an open network)")
	host := fl.String("host", "", "the address the pager dials (default: this Mac's <LocalHostName>.local; an IP always works)")
	yes := fl.Bool("yes", false, "don't ask whether the fingerprints match the pager's screen, for unattended tests; BOOT on the pager is still needed")
	if err := fl.Parse(args); err != nil {
		return 2
	}
	switch {
	case fl.NArg() > 0:
		fmt.Fprintf(c.stderr, "pager-bridge provision: unexpected arguments\n")
		return 2
	case *port == "" || *name == "" || len(ssids) == 0:
		fmt.Fprintf(c.stderr, "pager-bridge provision: --port, --name and at least one --wifi-ssid are needed\n")
		return 2
	case !*passStdin:
		fmt.Fprintf(c.stderr, "pager-bridge provision: give the passwords on stdin, with --wifi-pass-stdin; a password is never an argument\n")
		return 2
	}

	if err := provisionWith(c, *port, *name, ssids, *host, *yes); err != nil {
		fmt.Fprintf(c.stderr, "pager-bridge provision: %v\n", err)
		return 1
	}
	return 0
}

// provisionWith reads the passwords and the bridge's files, opens the
// terminal it asks on (unless yes), then pairs.
func provisionWith(c *cli, port, name string, ssids []string, host string, yes bool) error {
	nets, err := readPasswords(c.stdin, ssids)
	if err != nil {
		return err
	}
	confirm := func(context.Context) (bool, error) {
		fmt.Fprintf(c.stdout, "--yes: not asking whether the fingerprints match.\n")
		return true, nil
	}
	if !yes {
		tty, err := c.openTTY()
		if err != nil {
			return fmt.Errorf("there's no terminal to ask on whether the fingerprints match (%v). Run provision in a terminal, or pair through the setup page (pager-bridge setup). Nothing was sent to the pager", err)
		}
		defer tty.Close()
		confirm = askOn(tty)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := config.Dir(home)
	cfg, err := config.Load(filepath.Join(dir, config.File), config.Env{Home: home, Getenv: os.Getenv})
	if err != nil {
		return orInit(err)
	}
	hostKey, err := config.HostKey(dir)
	if err != nil {
		return orInit(err)
	}
	_, p, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return err
	}
	bridgePort, err := strconv.Atoi(p)
	if err != nil {
		return fmt.Errorf("config.toml: listen: port %q isn't a number", p)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return provision.Run(ctx, provision.Options{
		Port:          port,
		Name:          name,
		Networks:      nets,
		Host:          host,
		BridgePort:    bridgePort,
		HostKey:       hostKey,
		Devices:       filepath.Join(dir, devices.File),
		Out:           c.stdout,
		LocalHostName: provision.LocalHostName,
		KnownNetworks: provision.KnownNetworks,
		Confirm:       confirm,
	})
}

// openTTY opens the terminal provision asks on: c.tty, or /dev/tty.
func (c *cli) openTTY() (io.ReadWriteCloser, error) {
	if c.tty != nil {
		return c.tty()
	}
	return os.OpenFile("/dev/tty", os.O_RDWR, 0)
}

// askOn is provision's second confirmation (ADR 0011): once the pager has
// confirmed with BOOT, it asks on tty whether the fingerprints printed
// above match the pager's screen, and is true only for y or yes. It gives
// up when ctx ends: the pager has stopped waiting.
func askOn(tty io.ReadWriter) func(context.Context) (bool, error) {
	return func(ctx context.Context) (bool, error) {
		fmt.Fprintf(tty, "Do these match the pager's screen? [y/N] ")
		answer := make(chan string, 1)
		go func() {
			line, _ := bufio.NewReader(tty).ReadString('\n')
			answer <- line
		}()
		select {
		case a := <-answer:
			a = strings.ToLower(strings.TrimSpace(a))
			return a == "y" || a == "yes", nil
		case <-ctx.Done():
			fmt.Fprintf(tty, "\n")
			return false, ctx.Err()
		}
	}
}

// orInit adds what to do about a bridge file that isn't there.
func orInit(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: run `pager-bridge init` first", err)
	}
	return err
}

// readPasswords reads one password line from stdin for each network, in
// order, and nothing more: it reads a byte at a time and stops at the last
// password's newline, so it never waits for the end of stdin (parent 11.6).
// An empty line is an open network. It never prints a password.
func readPasswords(stdin io.Reader, ssids []string) ([]provision.Network, error) {
	nets := make([]provision.Network, 0, len(ssids))
	var line []byte
	b := make([]byte, 1)
	for len(nets) < len(ssids) {
		n, err := stdin.Read(b)
		switch {
		case n == 1 && b[0] == '\n':
			nets = append(nets, provision.Network{SSID: ssids[len(nets)], Pass: strings.TrimSuffix(string(line), "\r")})
			line = line[:0]
		case n == 1:
			line = append(line, b[0])
		case errors.Is(err, io.EOF):
			return nil, fmt.Errorf("stdin gives a password for %d of the %d networks: one line each, in --wifi-ssid order", len(nets), len(ssids))
		case err != nil:
			return nil, fmt.Errorf("reading the passwords on stdin: %w", err)
		}
	}
	return nets, nil
}

// ssidList is --wifi-ssid, which repeats.
type ssidList []string

func (s *ssidList) String() string { return strings.Join(*s, ",") }

func (s *ssidList) Set(v string) error {
	*s = append(*s, v)
	return nil
}
