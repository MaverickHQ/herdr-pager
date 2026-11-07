// Package keymaps holds the default keymap of each agent kind, one TOML file
// per kind (parent 23). Phase 0's recon wrote claude.toml from its captures,
// so no lane changes it.
package keymaps

import "embed"

// FS holds every keymap file, by name: claude.toml, ...
//
//go:embed *.toml
var FS embed.FS
