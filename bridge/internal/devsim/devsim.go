// Package devsim is a simulated pager for tests (seam S2-1): an SSH client
// with a device key, pinned to the bridge's host key, speaking the pager
// protocol one line at a time.
package devsim

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

// A Device is one simulated pager's session.
type Device struct {
	client *ssh.Client
	stdin  io.WriteCloser
	lines  chan string
	err    error // why lines closed, once it has
}

// ErrTimeout is a Recv that saw no line in time.
var ErrTimeout = errors.New("devsim: no line in time")

// ErrHostKeyChanged is a Dial to a bridge whose host key isn't the pinned one
// (parent 3.3): the pager shows HOST KEY CHANGED and never retries.
var ErrHostKeyChanged = errors.New("devsim: host key changed")

// DialTimeout bounds Dial: the connection, the handshake and opening the
// session channel.
const DialTimeout = 5 * time.Second

// Dial connects to the bridge at addr as key, accepting only hostKey, and
// opens the session channel as the pager does: one exec request, whose
// command the bridge ignores.
func Dial(addr string, key ssh.Signer, hostKey ssh.PublicKey) (*Device, error) {
	conn, err := net.DialTimeout("tcp", addr, DialTimeout)
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(time.Now().Add(DialTimeout))
	c, chans, reqs, err := ssh.NewClientConn(conn, addr, &ssh.ClientConfig{
		User: "pager",
		Auth: []ssh.AuthMethod{ssh.PublicKeys(key)},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if !bytes.Equal(key.Marshal(), hostKey.Marshal()) {
				return ErrHostKeyChanged
			}
			return nil
		},
	})
	if err != nil {
		conn.Close()
		return nil, err
	}
	client := ssh.NewClient(c, chans, reqs)
	d, err := open(client)
	if err != nil {
		client.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return d, nil
}

func open(client *ssh.Client) (*Device, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := session.Start("pager"); err != nil {
		return nil, err
	}
	d := &Device{client: client, stdin: stdin, lines: make(chan string, 64)}
	go d.read(stdout)
	return d, nil
}

func (d *Device) read(stdout io.Reader) {
	r := bufio.NewReaderSize(stdout, 16<<10)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			d.err = err
			close(d.lines)
			return
		}
		d.lines <- line[:len(line)-1]
	}
}

// Send writes one line; the '\n' is added.
func (d *Device) Send(line string) error {
	_, err := io.WriteString(d.stdin, line+"\n")
	return err
}

// Recv returns the next line from the bridge, without its '\n'. Once the
// bridge has closed the session it returns io.EOF, or the read's error.
func (d *Device) Recv(timeout time.Duration) (string, error) {
	select {
	case line, ok := <-d.lines:
		if !ok {
			return "", d.err
		}
		return line, nil
	case <-time.After(timeout):
		return "", ErrTimeout
	}
}

// Close ends the session.
func (d *Device) Close() error { return d.client.Close() }

// Another opens a second session channel on d's connection, as a pager never
// does: for tests of one session per key (parent 3.6). Closing either closes
// the connection.
func (d *Device) Another() (*Device, error) { return open(d.client) }
