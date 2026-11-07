package setup

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// The page's bridge channel (requirement 2.2, design §The setup page, Live
// output): each line appended to the bridge's log file, from the moment the
// page starts. The log is wherever the bridge runs at login writes it, so the
// page follows every file it may be in, and shows what any of them gains.

// TailEvery is how often the page reads the bridge's log files.
const TailEvery = time.Second

// The most the page reads from one file at a time: past it, it skips to the
// newest lines.
const maxTailRead = 64 << 10

// BridgeLogs are the files the bridge's log may be in, for a user whose home
// is home: brew services' log_path, in Homebrew's var/log
// (contrib/homebrew), and the launchd plist's (contrib/launchd).
func BridgeLogs(home string) []string {
	return []string{
		"/opt/homebrew/var/log/herdr-pager.log",
		filepath.Join(home, "Library", "Logs", "herdr-pager", "bridge.log"),
	}
}

// A tail follows one file.
type tail struct {
	path string
	off  int64  // how far it has read
	part []byte // a line not ended yet
}

// newTails starts following each of h's bridge logs at its end as it is
// now; one that isn't there, at its start once it is.
func (h *Handler) newTails() []*tail {
	tails := make([]*tail, len(h.opts.BridgeLogs))
	for i, path := range h.opts.BridgeLogs {
		tails[i] = &tail{path: path}
		if fi, err := os.Stat(path); err == nil && fi.Mode().IsRegular() {
			tails[i].off = fi.Size()
		}
	}
	return tails
}

// tailBridgeLogs reads tails every TailEvery until ctx ends, and shows each
// whole line they gain on the bridge channel.
func (h *Handler) tailBridgeLogs(ctx context.Context, tails []*tail) {
	if len(tails) == 0 {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-h.clock.After(TailEvery):
		}
		for _, t := range tails {
			for _, line := range t.read() {
				h.bridgeLine(line)
			}
		}
	}
}

// read returns the whole lines t's file has gained, without their endings.
// A file that's gone is read from its start when it's back, and one that
// shrank (emptied, or rotated) from its new start. A line longer than
// maxRead bytes is cut short.
func (t *tail) read() []string {
	f, err := os.Open(t.path)
	if err != nil {
		t.off, t.part = 0, nil
		return nil
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	size := fi.Size()
	if size < t.off {
		t.off, t.part = 0, nil
	}
	skip := size-t.off > maxTailRead // too much at once: only the newest lines
	if skip {
		t.off, t.part = size-maxTailRead, nil
	}
	if _, err := f.Seek(t.off, io.SeekStart); err != nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(f, size-t.off))
	if err != nil {
		return nil
	}
	t.off += int64(len(data))
	if i := bytes.IndexByte(data, '\n'); skip && i >= 0 {
		data = data[i+1:] // the end of a line it skipped the start of
	}
	var lines []string
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		lines = append(lines, string(cut(append(t.part, data[:i]...))))
		t.part, data = nil, data[i+1:]
	}
	t.part = cut(append(t.part, data...))
	return lines
}

// cut is line, cut to maxRead bytes.
func cut(line []byte) []byte {
	if len(line) > maxRead {
		return line[:maxRead]
	}
	return line
}

// maxRead is the longest line the page shows whole.
const maxRead = 4096

// debugLine matches a line the bridge logged at debug level: slog's text
// format, whose first level= is the line's own. At debug the bridge may log
// card text (config.toml), which the page never shows (requirement 2.7).
var debugLine = regexp.MustCompile(`^[^"]*?(^|\s)level=DEBUG([+-][0-9]+)?(\s|$)`)

// bridgeLine shows one of the bridge's log lines on the bridge channel, as
// plain text, unless the bridge logged it at debug level.
func (h *Handler) bridgeLine(line string) {
	if debugLine.MatchString(line) {
		return
	}
	h.hub.publish(event{chanBridge, plainText(line)})
}
