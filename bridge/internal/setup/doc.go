// Package setup is the setup page that `pager-bridge setup` serves on
// 127.0.0.1 behind a token (ADR 0009): it checks the Mac with the doctor,
// flashes the shipped firmware with ESP Web Tools, pairs a pager over USB
// through provision, and manages the paired pagers and run at login. It never
// reaches an agent (TestTheSetupPageCantReachAnAgent).
package setup
