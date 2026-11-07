// Launches the check must refuse: each line marked `refused` is one.
package refused

import (
	"context"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

func shell(text string) {
	exec.Command("sh", "-c", text).Run() // refused: an argument from a caller that isn't checked
}

func other() {
	exec.Command("open", "-a", "Safari").Run() // refused: fixed, but not scutil
}

func viaContext(ctx context.Context, name string) {
	exec.CommandContext(ctx, name).Run() // refused
}

func run(argv ...string) {
	exec.Command(argv[0], argv[1:]...).Run() // refused: one call passes a variable
}

func callsRun(pane string) {
	run("/usr/sbin/scutil", "--get", "LocalHostName")
	run("echo", pane)
}

func passesRun() func(...string) {
	return run // and run escapes, so its calls can't all be seen
}

func built(args []string) {
	exec.Command("/usr/sbin/scutil", append([]string{"--get"}, args...)...).Run() // refused
}

func stored() {
	f := func(argv ...string) { exec.Command(argv[0], argv[1:]...).Run() } // refused: not passed straight to a function
	f("/usr/sbin/scutil", "--get", "LocalHostName")
}

func lowLevel() {
	os.StartProcess("/usr/sbin/scutil", []string{"scutil", "--get", "LocalHostName"}, nil) // refused
	syscall.Exec("/usr/sbin/scutil", []string{"scutil", "--get", "LocalHostName"}, nil)     // refused
	_ = exec.Cmd{Path: "/usr/sbin/scutil"}                                                  // refused
}

func fromXSys() {
	unix.Exec("/usr/sbin/scutil", []string{"scutil", "--get", "LocalHostName"}, nil) // refused: the module depends on x/sys
}

func byHand() *exec.Cmd { // a *exec.Cmd is fine: it's how exec.Command's result is passed
	var c exec.Cmd // refused: an exec.Cmd built by hand
	c.Path = "/usr/sbin/scutil"
	return new(exec.Cmd) // refused
}

func asValue() {
	launch := exec.Command // refused: its calls can't all be seen
	launch("/usr/sbin/scutil", "--get", "LocalHostName").Run()
}
