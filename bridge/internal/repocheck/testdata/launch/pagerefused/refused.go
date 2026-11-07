// Launches of open the check must refuse in internal/setup, beside that
// package's own pageURL: each line marked `refused` is one.
package setup

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

func pageURL(port int, token string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/?t=%s", port, token)
}

func otherURL(port int, token string) string {
	return fmt.Sprintf("http://example.com:%d/?t=%s", port, token)
}

type server struct {
	port  int
	token string
}

func (s *server) pageURL() string { return pageURL(s.port, s.token) }

func literal() {
	exec.Command("/usr/bin/open", "http://127.0.0.1:2277/").Run() // refused: a literal, which can't be the page's URL (its port and token are random)
}

func byName(port int, token string) {
	exec.Command("open", pageURL(port, token)).Run() // refused: open by name, not /usr/bin/open
}

func stored(port int, token string) {
	u := pageURL(port, token)
	exec.Command("/usr/bin/open", u).Run() // refused: a variable, not pageURL's result
}

func wrapped(port int, token string) {
	exec.Command("/usr/bin/open", strings.TrimSpace(pageURL(port, token))).Run() // refused: pageURL's result through another function
}

func another(port int, token string) {
	exec.Command("/usr/bin/open", otherURL(port, token)).Run() // refused: another function's result
}

func openURL(u string) {
	exec.Command("/usr/bin/open", u).Run() // refused: a parameter, though its one call passes pageURL's result
}

func callsOpenURL(port int, token string) {
	openURL(pageURL(port, token))
}

func launch(run func(argv ...string) error, port int, token string) error {
	return run("/usr/bin/open", pageURL(port, token))
}

func byValue(port int, token string) error {
	return launch(func(argv ...string) error {
		return exec.Command(argv[0], argv[1:]...).Run() // refused: a function value that's called with pageURL's result
	}, port, token)
}

func extra(ctx context.Context, port int, token string) {
	exec.Command("/usr/bin/open", "-a", "Safari", pageURL(port, token)).Run()       // refused: more than the URL
	exec.CommandContext(ctx, "/usr/bin/open", pageURL(port, token), "--args").Run() // refused
}

func shadowedByParameter(pageURL func(int, string) string) {
	exec.Command("/usr/bin/open", pageURL(1, "t")).Run() // refused: a parameter named pageURL
}

func shadowedByVariable(port int, token string) {
	pageURL := otherURL
	exec.Command("/usr/bin/open", pageURL(port, token)).Run() // refused: a variable named pageURL
}

func (s *server) method() {
	exec.Command("/usr/bin/open", s.pageURL()).Run() // refused: a method named pageURL
}

func otherProgram(port int, token string) {
	exec.Command("/usr/sbin/scutil", pageURL(port, token)).Run() // refused: pageURL's result to another program
	exec.Command(pageURL(port, token)).Run()                     // refused: pageURL's result as the program
}
