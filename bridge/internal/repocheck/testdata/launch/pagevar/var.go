// pageURL as a variable, not a function: it could hold anything at run time,
// so the check refuses open with its result in internal/setup, in the file
// that declares it and in another (open.go). Each line marked `refused` is
// one.
package setup

import "os/exec"

var pageURL = func(port int, token string) string { return "http://127.0.0.1/" }

func openPage(port int, token string) {
	exec.Command("/usr/bin/open", pageURL(port, token)).Run() // refused
}
