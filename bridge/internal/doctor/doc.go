// Package doctor is the Mac's checks (phase 5 design D5P-6, requirement 2.8):
// herdr's socket, the bridge's keys, its port, the firewall, the login item,
// and the paired devices and whether each is connected. `pager-bridge doctor`,
// the setup page and the Claude Code skill all ask it, so the terminal, the
// page and the skill give the same answer.
//
// It only reads: the bridge's directory and status.json, a dial to herdr's
// socket and to the bridge's port, and commands whose every word is fixed:
// socketfilterfw's global state and list of apps (where it looks for the
// bridge's own path), and `brew services list --json`. A check that fails
// says how to fix it, and lists the commands that would; the doctor never
// runs them. Nothing it reads or prints comes from an agent or a pager,
// beyond the paired devices' names.
//
// `pager-bridge doctor --json` prints a Report: {"ok", "checks": [{"id",
// "name", "ok", "state", "detail", "fix", "commands", "devices": [{"name",
// "fingerprint", "connected"}]}]}. The ids and states are the constants in
// doctor.go; TestDoctorPrintsItsReport pins the field names.
package doctor
