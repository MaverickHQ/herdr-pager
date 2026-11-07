package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

var stdioCmd = command{"stdio", []string{
	"stdio", "Speak the pager protocol on stdin and stdout, for one session.",
}, runStdio}

// runStdio runs one pager session on stdin and stdout, exactly as over SSH
// (parent 11.3; lane e2e, task 4.26): for tests, and for future use as an
// OpenSSH forced command.
//
// It trusts whoever runs it, as a forced command trusts sshd to have
// authenticated the device, and logs to stderr. It polls herdr as serve does
// and runs the same session. It exits 0 when stdin ends, the session goes
// idle, or on SIGINT or SIGTERM; and 1 when the session ends for another
// reason (no hello, a line over 512 bytes, five invalid messages).
func runStdio(c *cli, args []string) int {
	b, failed := loadBridge(c, "stdio", args)
	if b == nil {
		return failed
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	br := b.start()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); br.Watch.Run(ctx) }()
	err := br.Run(ctx, c.stdin, c.stdout)
	cancel()
	stopWithin(&wg, b.log, "stdio")
	if err != nil {
		b.log.Info("stdio: the session ended", "err", err)
		return 1
	}
	return 0
}
