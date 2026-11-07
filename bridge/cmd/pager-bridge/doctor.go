package main

import (
	"encoding/json"
	"flag"
	"fmt"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/doctor"
)

// runDoctor runs pager-bridge doctor (doctorCmd, in main.go): the Mac's
// checks, from bridge/internal/doctor, the one source the setup page and the
// Claude Code skill use too (phase 5 design D5P-6, requirement 2.8). It
// prints each check's verdict and, for each that fails, its fix; --json
// prints the same as JSON. It exits 1 if any check fails, so a script can
// tell, and 2 on a bad flag or an argument.
func runDoctor(c *cli, args []string) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	asJSON := flags.Bool("json", false, "print the checks as JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 0 {
		fmt.Fprintln(c.stderr, "pager-bridge doctor: takes no arguments")
		return 2
	}
	env, err := doctor.System()
	if err != nil {
		fmt.Fprintf(c.stderr, "pager-bridge doctor: %v\n", err)
		return 1
	}
	r := doctor.Run(env)
	if *asJSON {
		data, err := json.Marshal(r)
		if err != nil {
			fmt.Fprintf(c.stderr, "pager-bridge doctor: %v\n", err)
			return 1
		}
		fmt.Fprintf(c.stdout, "%s\n", data)
	} else if err := r.WriteText(c.stdout); err != nil {
		fmt.Fprintf(c.stderr, "pager-bridge doctor: %v\n", err)
		return 1
	}
	if !r.OK {
		return 1
	}
	return 0
}
