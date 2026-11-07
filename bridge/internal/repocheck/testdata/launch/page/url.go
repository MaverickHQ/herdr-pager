// The setup page's one launch of open (phase 5 design D5P-3 §Never, task
// 3.11), as bridge/internal/setup may write it: "/usr/bin/open" and the
// direct result of pageURL, that package's own function. The check allows
// these launches in internal/setup, and refuses each in any other package
// (the marks).
package setup

import (
	"fmt"
	"net/url"
	"os/exec"
)

// pageURL is the page's URL: 127.0.0.1, the listener's port and the token.
func pageURL(port int, token string) string {
	return fmt.Sprintf("http://127.0.0.1:%d/?t=%s", port, url.QueryEscape(token))
}

// pageURL called in the file that declares it.
func openPage(port int, token string) error {
	return exec.Command("/usr/bin/open", pageURL(port, token)).Run() // refused in any package but internal/setup
}
