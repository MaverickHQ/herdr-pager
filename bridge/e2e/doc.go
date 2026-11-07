// Package e2e holds the bridge's end-to-end tests (seam S2-1, design D2-3):
// devsim, a simulated pager, over real loopback SSH to the bridge, and the
// bridge to a fake herdr. Every test runs with a temporary HOME, no
// HERDR_SOCKET_PATH, and its own fake herdr socket in a short temp folder.
package e2e
