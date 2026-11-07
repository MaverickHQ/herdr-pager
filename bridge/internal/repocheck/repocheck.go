// Package repocheck holds the tests that keep the repository's security rules
// (lane e2e, task 4.27): the bridge's only process launches are
// `scutil --get LocalHostName` and `networksetup
// -listpreferredwirelessnetworks` on en0 or en1, and from phase 5 the
// doctor's `socketfilterfw` and `brew services` and run at login's `brew
// services start|stop herdr-pager`, each with a fixed argument vector, but
// for `socketfilterfw --getappblocked` and the bridge's own path, the direct
// result of internal/doctor's ownPath (the owner's checkpoint 7.2); and
// the setup page's `open` of its own URL, the direct result of
// internal/setup's pageURL (parent 13.3; phase 5 design D5P-3 §Never); and
// its only crypto imports are golang.org/x/crypto/ssh, crypto/ed25519,
// crypto/sha256 and crypto/rand (parent 13.7).
package repocheck
