package config

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

// HostKeyFile is the bridge's ed25519 SSH host key in its directory (0600, an
// OpenSSH private key), beside HostKeyFile+".pub" (0644, one authorized_keys
// line).
const HostKeyFile = "host_ed25519"

// defaultFile is the config.toml that init writes: every setting at its
// default (parent design §Data models).
const defaultFile = `# pager-bridge's settings, written by ` + "`pager-bridge init`" + `.
# An empty or missing value takes its default.

# The address the SSH server listens on.
listen       = "0.0.0.0:2277"

# herdr's socket. Empty: $HERDR_SOCKET_PATH, else ~/.config/herdr/herdr.sock.
herdr_socket = ""

# A device's session closes after this long without a message (it pings every 60 s).
idle_timeout = "180s"

# info or debug. info never logs card text, detection text or agent names.
log_level    = "info"
`

// Init makes sure the bridge's directory, dir (0700), holds an ed25519 host
// key and a config.toml (0600), and returns the host key's public half
// (parent 1.1, 13.9). It changes no file that exists, and so it keeps the
// key: it only creates what's missing, such as the .pub from the key that's
// there (parent 1.3).
func Init(dir string) (ssh.PublicKey, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, HostKeyFile)
	pub, err := hostKey(path)
	if err != nil {
		return nil, err
	}
	if err := createFile(path+".pub", ssh.MarshalAuthorizedKey(pub), 0o644); err != nil {
		return nil, err
	}
	if err := createFile(filepath.Join(dir, File), []byte(defaultFile), 0o600); err != nil {
		return nil, err
	}
	return pub, nil
}

// HostKey is the public half of the bridge's host key in dir. A missing key
// is an error wrapping fs.ErrNotExist: init makes it.
func HostKey(dir string) (ssh.PublicKey, error) {
	path := filepath.Join(dir, HostKeyFile)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseHostKey(path, data)
}

// hostKey reads the ed25519 host key at path, or creates a new one there.
func hostKey(path string) (ssh.PublicKey, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := newHostKey(path); err != nil {
			return nil, err
		}
		data, err = os.ReadFile(path) // another init may have made it first
	}
	if err != nil {
		return nil, err
	}
	return parseHostKey(path, data)
}

// parseHostKey reads data, the host key at path, as an ed25519 private key,
// and returns its public half.
func parseHostKey(path string, data []byte) (ssh.PublicKey, error) {
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if t := signer.PublicKey().Type(); t != ssh.KeyAlgoED25519 {
		return nil, fmt.Errorf("%s: an %s key; the host key is ssh-ed25519", path, t)
	}
	return signer.PublicKey(), nil
}

// newHostKey creates a new ed25519 host key at path, unless one is there.
func newHostKey(path string) error {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return err
	}
	return createFile(path, pem.EncodeToMemory(block), 0o600)
}

// createFile creates path holding data with exactly mode, whatever the umask,
// unless path exists, when it leaves it alone. The file appears whole or not
// at all: it's written as a temporary file and linked into place.
func createFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".init-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return nil
}
