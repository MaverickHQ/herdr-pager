package setup

import (
	"context"
	osexec "os/exec"
)

type server struct {
	port  int
	token string
}

// pageURL called from another file of the package, in a method, with a
// context, under another import name.
func (s *server) open(ctx context.Context) error {
	return osexec.CommandContext(ctx, "/usr/bin/open", pageURL(s.port, s.token)).Run() // refused in any package but internal/setup
}
