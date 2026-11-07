// Command pager-bridge is herdr-pager's bridge (parent design §Bridge
// components): it serves paired pagers over SSH, and it's the only thing that
// talks to herdr's socket.
//
// Each subcommand lives in its own file, with its lines in the help. This one
// only dispatches, with the standard library alone: each command parses its
// own flags with package flag.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/version"
)

// A command is one subcommand: its lines in the help, and what runs it.
type command struct {
	name  string
	usage []string // pairs: the synopsis after "pager-bridge ", then what it does
	run   func(c *cli, args []string) int
}

// cli is what a command runs with. Commands write to these, never to os.Stdout
// or os.Stderr. Only provision (its passwords) and stdio (its session) read
// stdin (parent 11.6).
type cli struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	// tty opens the terminal for provision's question, whether the
	// fingerprints match (its stdin carries the passwords); nil for
	// /dev/tty.
	tty func() (io.ReadWriteCloser, error)
}

var commands = []command{initCmd, serveCmd, stdioCmd, statusCmd, provisionCmd, devicesCmd, setupCmd, doctorCmd}

// setup's and doctor's entries are here, not in their own files, so the lane
// that writes runSetup (setup.go) and runDoctor (doctor.go) never edits this
// one (phase 5 task 1.4; requirements 2.1 and 2.8).
var setupCmd = command{"setup", []string{
	"setup [--firmware <dir>]", "Open the setup page in the browser, on 127.0.0.1 only: check the Mac, flash the pager, its Wi-Fi, pair it, the firewall and run at login; then manage them. It stops 5 minutes after its last tab closes, or on Ctrl-C.",
}, runSetup}

var doctorCmd = command{"doctor", []string{
	"doctor [--json]", "Check the Mac's setup: herdr's socket, the bridge's keys, port 2277, the firewall, the login item, and the paired devices and which are connected; each check that fails says how to fix it.",
}, runDoctor}

func main() {
	os.Exit(run(&cli{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}, os.Args[1:]))
}

func run(c *cli, args []string) int {
	if len(args) == 0 {
		usage(c.stderr)
		return 2
	}
	switch args[0] {
	case "help", "-h", "-help", "--help":
		usage(c.stdout)
		return 0
	case "--version", "-version":
		fmt.Fprintf(c.stdout, "pager-bridge %s\n", version.String())
		return 0
	}
	for _, cmd := range commands {
		if cmd.name == args[0] {
			return cmd.run(c, args[1:])
		}
	}
	fmt.Fprintf(c.stderr, "pager-bridge: no command %q\n", args[0])
	usage(c.stderr)
	return 2
}

func usage(w io.Writer) {
	fmt.Fprint(w, "pager-bridge: herdr-pager's bridge.\n\nUsage:\n")
	for _, cmd := range commands {
		for i := 0; i < len(cmd.usage); i += 2 {
			fmt.Fprintf(w, "\n  pager-bridge %s\n      %s\n", cmd.usage[i], cmd.usage[i+1])
		}
	}
	fmt.Fprint(w, "\n  pager-bridge --version\n      Print the bridge's version: a published version's number, or <number>-dev for any other build.\n")
	fmt.Fprint(w, "\nThe pager is an attention device, not a security gate: any process in a herdr pane can already drive herdr's socket.\n")
}
