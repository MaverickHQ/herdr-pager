// Crypto imports the check must refuse: each line marked `refused` is one.
// The others are the ones parent 13.7 allows (crypto/subtle, the standard
// library's constant-time comparison, since open-items O-93).
package imports

import (
	"crypto/aes" // refused
	"crypto/ed25519"
	"crypto/hmac" // refused
	"crypto/md5"  // refused
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"golang.org/x/crypto/chacha20poly1305" // refused
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent" // refused
)
