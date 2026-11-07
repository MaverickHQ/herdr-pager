package herdr

import (
	"bufio"
	"cmp"
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"strings"
	"syscall"
	"time"
	"unicode"
)

// A Client calls herdr's socket: one request per connection, one line each
// way, and a 2 s deadline (phase 2 R5.1).
type Client struct {
	SocketPath string
}

// Timeout bounds each call: herdr sets no deadline of its own for the five
// methods, so this is the only one (parent design §Bridge components). A call
// that runs out of it fails with an error wrapping os.ErrDeadlineExceeded.
const Timeout = 2 * time.Second

// maxReply bounds a reply line; agent.read's detection text is a screen or two.
const maxReply = 4 << 20

// DisplayName is herdr's name for a, else its terminal title, else "<kind>
// <pane id>" (phase 2 R5.2; the title since the desk checkpoint, 2026-10-24),
// unsanitized. A title loses its leading status glyph and spaces (Claude Code
// starts it with ✳ or a spinner), and one with nothing else in it is none.
func (a AgentInfo) DisplayName() string {
	if a.Name != nil && *a.Name != "" {
		return *a.Name
	}
	if a.TitleStripped != nil {
		if t := strings.TrimLeftFunc(*a.TitleStripped, notWordStart); t != "" {
			return t
		}
	}
	kind := ""
	if a.Agent != nil {
		kind = *a.Agent
	}
	return kind + " " + a.PaneID
}

// Code names why a call failed, for logs (parent 4.4): herdr's own error
// code, or socket_missing, socket_refused, socket_permission, timeout,
// bad_reply or unreachable. It never quotes herdr's message, which can carry
// a pane's target, and it passes herdr's code only if it has the shape of
// one (codeShape), else `other`.
func Code(err error) string {
	var herr *Error
	switch {
	case errors.As(err, &herr):
		return herr.safeCode()
	case errors.Is(err, ErrBadReply):
		return "bad_reply"
	case errors.Is(err, os.ErrDeadlineExceeded), errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, fs.ErrNotExist):
		return "socket_missing"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "socket_refused"
	case errors.Is(err, fs.ErrPermission):
		return "socket_permission"
	}
	return "unreachable"
}

// ListAgents calls agent.list.
func (c *Client) ListAgents(ctx context.Context) ([]AgentInfo, error) {
	line, err := c.call(ctx, MethodList, Empty{})
	if err != nil {
		return nil, err
	}
	return decodeList(line)
}

// ListTabs calls tab.list: every tab of every workspace.
func (c *Client) ListTabs(ctx context.Context) ([]TabInfo, error) {
	line, err := c.call(ctx, MethodTabs, Empty{})
	if err != nil {
		return nil, err
	}
	return decodeTabs(line)
}

// ListWorkspaces calls workspace.list.
func (c *Client) ListWorkspaces(ctx context.Context) ([]WorkspaceInfo, error) {
	line, err := c.call(ctx, MethodWorkspaces, Empty{})
	if err != nil {
		return nil, err
	}
	return decodeWorkspaces(line)
}

// GetAgent calls agent.get for pane.
func (c *Client) GetAgent(ctx context.Context, pane string) (AgentInfo, error) {
	line, err := c.call(ctx, MethodGet, Target{Target: pane})
	if err != nil {
		return AgentInfo{}, err
	}
	return decodeAgent(line)
}

// ReadDetection calls agent.read for pane, source detection, format text.
func (c *Client) ReadDetection(ctx context.Context, pane string) (string, error) {
	line, err := c.call(ctx, MethodRead, ReadParams{Target: pane, Source: "detection", Format: "text"})
	if err != nil {
		return "", err
	}
	read, err := decodeRead(line)
	return read.Text, err
}

// SendKeys calls agent.send_keys for pane with keys from a keymap. Success
// means herdr queued the keys to the pane.
func (c *Client) SendKeys(ctx context.Context, pane string, keys []string) error {
	line, err := c.call(ctx, MethodSendKeys, SendKeysParams{Target: pane, Keys: keys})
	if err != nil {
		return err
	}
	return decodeOK(line)
}

// Focus calls agent.focus for pane.
func (c *Client) Focus(ctx context.Context, pane string) error {
	line, err := c.call(ctx, MethodFocus, Target{Target: pane})
	if err != nil {
		return err
	}
	_, err = decodeAgent(line)
	return err
}

// call sends one request line on its own connection and returns herdr's one
// reply line, all within Timeout (or ctx's deadline, if sooner). If ctx is
// cancelled first, the call ends then, with ctx's error.
func (c *Client) call(ctx context.Context, method string, params any) ([]byte, error) {
	req, err := newRequest(method, params)
	if err != nil {
		return nil, err
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline, _ := ctx.Deadline()
	conn.SetDeadline(deadline)
	defer context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })()
	var line []byte
	if _, err = conn.Write(req); err == nil {
		line, err = bufio.NewReader(io.LimitReader(conn, maxReply)).ReadBytes('\n')
	}
	if err != nil && errors.Is(parent.Err(), context.Canceled) {
		return nil, parent.Err()
	}
	return line, err
}

// Folder is the name of the folder a works in: its foreground process's
// working directory, else its pane's, else "" (the desk checkpoint,
// 2026-10-24). Unsanitized.
func (a AgentInfo) Folder() string {
	dir := strings.TrimRight(cmp.Or(a.ForegroundCwd, a.Cwd), "/")
	if dir == "" {
		return ""
	}
	return path.Base(dir)
}

// notWordStart says whether r can't start a title: anything but a letter or
// a digit, such as a status glyph or a space.
func notWordStart(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }
