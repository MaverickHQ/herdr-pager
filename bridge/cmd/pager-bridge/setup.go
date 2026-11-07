package main

import "github.com/MaverickHQ/herdr-pager/bridge/internal/setup"

// runSetup runs pager-bridge setup (setupCmd, in main.go): the setup page,
// bridge/internal/setup (phase 5 design D5P-3, task 3.11).
func runSetup(c *cli, args []string) int {
	return setup.Main(c.stdout, c.stderr, args)
}
