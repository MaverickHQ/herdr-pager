// Package bridge wires the bridge's parts together, once, for everything that
// runs it: serve, stdio and the end-to-end tests. A watcher of herdr feeds the
// sessions; serve and the tests run those sessions on the SSH server's
// channels, stdio on stdin and stdout.
package bridge

import (
	"context"
	"io"
	"log/slog"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/MaverickHQ/herdr-pager/bridge/internal/clock"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/devices"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/herdr"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/keymap"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/risk"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/session"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/sshserver"
	"github.com/MaverickHQ/herdr-pager/bridge/internal/watch"
)

// Config is what a bridge is built from.
type Config struct {
	HerdrSocket string // herdr's socket
	Keymaps     keymap.Set
	Rules       risk.Rules
	IdleTimeout time.Duration // config.toml's idle_timeout; the session's default if zero
	Host        string        // the bridge's host name for its hello
	Clock       clock.Clock   // clock.Real{}, but for tests
	Log         *slog.Logger
}

// A Bridge is a watcher of herdr and the sessions it feeds.
type Bridge struct {
	Watch *watch.Watcher // run it for the sessions to get snapshots
	deps  session.Deps
	clock clock.Clock
	log   *slog.Logger
}

// New builds a bridge from c (the owner's decision, 2026-10-27, open-items
// O-36: serve, stdio and the end-to-end tests share this wiring).
func New(c Config) *Bridge {
	client := &herdr.Client{SocketPath: c.HerdrSocket}
	w := watch.New(watch.Config{Herdr: client, Keymaps: c.Keymaps, Clock: c.Clock, Log: c.Log})
	return &Bridge{
		Watch: w,
		deps: session.Deps{
			Herdr: client, Watch: w, Keymaps: c.Keymaps, Rules: c.Rules, Locks: &session.Locks{},
			Clock: c.Clock, Log: c.Log, Host: c.Host, IdleTimeout: c.IdleTimeout,
		},
		clock: c.Clock,
		log:   c.Log,
	}
}

// Run runs one session on in and out until it ends or ctx does.
func (b *Bridge) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	return session.New(b.deps).Run(ctx, in, out)
}

// Server is the SSH server whose sessions b runs, with hostKey, for the
// devices paired returns. opened, if not nil, is told of each session as it
// opens (+1) and ends (-1), by the device's name.
func (b *Bridge) Server(hostKey ssh.Signer, paired func() []devices.Device, opened func(name string, n int)) *sshserver.Server {
	return &sshserver.Server{
		HostKey: hostKey,
		Devices: paired,
		Handle: func(ctx context.Context, d devices.Device, ch io.ReadWriter) {
			if opened != nil {
				opened(d.Name, 1)
				defer opened(d.Name, -1)
			}
			b.Run(ctx, ch, ch)
		},
		Clock: b.clock,
		Log:   b.log,
	}
}
