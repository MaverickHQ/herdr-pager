package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/config"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
)

var initCmd = command{"init", []string{
	"init", "Create the bridge's host key, config.toml and devices.toml in ~/.config/herdr-pager.",
}, runInit}

// runInit creates the bridge's identity in a 0700 directory: the ed25519 host
// key (0600, its .pub 0644), config.toml and an empty devices.toml; it prints
// the full SHA256 fingerprint and the listen address, and warns when herdr's
// socket is missing. Running it again changes nothing (parent 1.1–1.5; lane
// cli, tasks 2.1–2.3).
func runInit(c *cli, args []string) int {
	if len(args) > 0 {
		fmt.Fprintf(c.stderr, "pager-bridge init: takes no arguments\n")
		return 2
	}
	home, err := os.UserHomeDir()
	if err != nil {
		fmt.Fprintf(c.stderr, "pager-bridge init: %v\n", err)
		return 1
	}
	dir := config.Dir(home)
	pub, err := config.Init(dir)
	if err == nil {
		err = devices.Create(filepath.Join(dir, devices.File))
	}
	var cfg config.Config
	if err == nil {
		cfg, err = config.Load(filepath.Join(dir, config.File), config.Env{Home: home, Getenv: os.Getenv})
	}
	if err != nil {
		fmt.Fprintf(c.stderr, "pager-bridge init: %v\n", err)
		return 1
	}
	fmt.Fprintf(c.stdout, "Bridge directory: %s\nHost key: %s (%s)\nListen address: %s\n",
		dir, ssh.FingerprintSHA256(pub), pub.Type(), cfg.Listen)
	if err := reachable(cfg.HerdrSocket); err != nil {
		fmt.Fprintf(c.stderr, "pager-bridge init: warning: herdr isn't reachable at %s (%v). Start herdr; serve keeps trying.\n",
			cfg.HerdrSocket, err)
	}
	return 0
}

// reachable reports whether something listens on herdr's socket at path. It
// connects and hangs up at once, sending nothing: init calls no herdr method.
func reachable(path string) error {
	conn, err := net.DialTimeout("unix", path, time.Second)
	if err != nil {
		if se := (*os.SyscallError)(nil); errors.As(err, &se) {
			return se.Err // "no such file or directory", "connection refused"
		}
		return err
	}
	return conn.Close()
}
