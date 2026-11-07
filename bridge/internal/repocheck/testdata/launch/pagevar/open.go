package setup

import "os/exec"

func openFromAnotherFile(port int, token string) {
	exec.Command("/usr/bin/open", pageURL(port, token)).Run() // refused
}
