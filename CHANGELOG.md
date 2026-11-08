# Changelog

All notable changes to herdr-pager are listed here. The pager's firmware and the bridge are versioned together, and each published version's entry is also its release notes.

## v1.0.0 (unreleased)

The first published version: **the desk pager**, made for a newcomer to set up alone. It runs beside a Mac with Apple Silicon, on USB power and the Mac's Wi-Fi, and pages for the Claude Code agents in [herdr](https://github.com/herdrdev/herdr). Its maintainer uses it daily, and the numbers in the README were measured on that board.

Before it there was only v0.1.0 (2026-10-26), a local tag for the desk pager's first week in use, never published. The lists below the first say what changed since.

### What's in it

- **The pager** (Waveshare ESP32-S3-Touch-LCD-3.49, in Case B): every herdr agent on one list, the most urgent first, with its herdr tab and folder, in one-character codes (`!` blocked, `*` done, `>` working, `?` unknown, `.` idle).
  - It pages when an agent has been blocked for 3 s, reminds every 5 minutes, and goes quiet when you answer at the keyboard. A short tone when an agent finishes a turn in a pane you aren't watching (DONE TONE turns it off).
  - Cards for a Bash command, a file edit or new file, a read outside the folder, a web fetch (its URL, and its host whole on the next line) and an MCP tool call (its server and tool, and up to 3 arguments, one a line), with what the agent asks to do on the first line.
  - Approve is a 1 s BOOT hold (2 s on a danger card) on a card that has been on screen for 500 ms; PWR denies. Touch never approves, denies or wakes the screen, and the press that wakes it does nothing else. There's no "always allow", and a verdict is never queued or sent again.
  - Jump to an agent's pane in herdr, or interrupt it while it works, after asking. Silent mode, volume, brightness, the done tone, a deep orange or a backlit green, device info, Pair again and factory reset.
- **pager-bridge**, one binary on the Mac: herdr's only client (`agent.list`, `agent.get`, `agent.read`, `agent.send_keys`, `agent.focus`, and the read-only `tab.list` and `workspace.list`), and an SSH server on port 2277 that lets in only paired pagers' full keys.
  - Every approval is checked again against the agent's `state_change_seq` and a hash of the card's text before a key is sent; if the prompt changed, nothing is sent and the pager says SCREEN CHANGED.
  - Keys come only from the agent's keymap (Claude Code's: `bridge/keymaps/claude.toml`), never text from the pager.
  - 17 danger rules (`docs/guides/danger-rules.md`) put a DANGER banner, a tone of its own and a 2 s hold on force pushes, recursive deletes, `curl … | sh`, `sudo`, SQL drops, writes to `~/.ssh` or `.env`, reads or writes of files that hold secrets, fetches of local addresses and more. Your own rules go in `~/.config/herdr-pager/risk.toml`.
  - A permission card the bridge can't read in full says COMMAND NOT READ (FILE, URL or TOOL NOT READ) and offers deny only.
- **The setup page:** `pager-bridge setup` serves a page on 127.0.0.1, behind a one-time token, that checks the Mac, flashes the pager over USB with the firmware shipped beside the bridge (ESP Web Tools, in Chrome or Edge), takes the Wi-Fi networks, pairs (both fingerprints on the page and the pager, confirmed by BOOT and then on the page; no Wi-Fi password reaches the pager before both), checks the firewall, turns on run at login and waits for the first page, showing the live output of each step, the bridge's log and the pager's. Afterwards it manages the setup: pair again, revoke, update the firmware (keeping the pairing), run at login. It never approves, denies or sends keys, and never shows a card; a Wi-Fi password reaches only the pager. It stops 5 minutes after its last tab closes.
- **`pager-bridge doctor [--json]`:** each check of the Mac's setup (herdr's socket, the bridge's keys, port 2277, the firewall, the login item, the paired pagers and which are connected), with a fix for each that fails. The page and the Claude Code skill use it too.
- **Install:** `brew install maverickhq/tap/herdr-pager`, then `pager-bridge setup`; `brew services start herdr-pager` runs the bridge at login. Each release, built by CI from its tag, has `herdr-pager_1.0.0_darwin_arm64.tar.gz` (the bridge, and the firmware's images with ESP Web Tools' manifest), `SHA256SUMS`, a build attestation for each file (`gh attestation verify`), `THIRD_PARTY.md`, and libssh 0.12.2's source with its build files (LGPL-2.1).
- **The Claude Code plugin:** `/plugin marketplace add MaverickHQ/herdr-pager`, then `/plugin install herdr-pager@herdr-pager`. Its skill checks the install, runs `pager-bridge doctor --json` and explains what fails, opens the setup page for anything that needs you, and explains the pager's screens. It never handles a Wi-Fi password, and never pairs or revokes a pager itself. No MCP server.
- **Guides:** pairing, the hardware, a phone's hotspot and the danger rules (`docs/guides/`); `docs/bridge.md` for every command; the keymap guide in `CONTRIBUTING.md`; the threat model's summary in `docs/threat-model.md`.

### Security (since v0.1.0)

- **The pager makes its device key with its radio on,** so the chip's random number generator draws on real entropy; libssh's generator is reseeded first. Before, an unpaired pager made its key before any entropy source was on.
- **A permission card offers approve only when the bridge has read it in full:** its command, path, URL or MCP call read by the keymap, with no escape code, control character or invisible character in it, and the option approve presses confirmed to be a plain "Yes", asked by exactly one question line. Anything else fails closed: deny only. The prompt box's title decides which pattern reads it, and decoy lines in a command, a fetch's prompt or a tool's description can't make a card approvable.
- **The danger rules** gain SECRETS (files that hold secrets, the bridge's own host key among them) and LOCAL ADDRESS (fetches of localhost, the private ranges and the cloud metadata address), and read every head both as it is and with its quotes and escapes as spaces, so `bash -c "sudo …"` is flagged.
- **Connections before authentication are held to little:** at most 32 in their SSH handshake, 4 from one address, 15 s each to start a session; a full set of slots makes way for an address holding fewer. Running out of file descriptors no longer stops `serve`, and what they cause is logged at most once a second per kind.
- **The sanitizer** stays linear on text full of unterminated control strings, and an unterminated string or a C1 code point hides nothing after it.
- `devices.toml`: adding and revoking take turns under a lock, and a `devices.toml` that won't load lets no pager in.
- HOST KEY CHANGED: the pager never authenticates to a changed host key, and dials again every 5 minutes in case its own bridge answers.
- The docs say to plug the pager only into computers you trust, and what to do if it was plugged into one you don't (README §Safety).

### Added (since v0.1.0)

- Cards for web fetches, reads outside the agent's folder and MCP tool calls (above). A bridge sends a 4-line card (an MCP call with 3 arguments) only to a pager whose hello says it draws 4.
- **The empty list says why:** WAITING FOR HERDR, HERDR OFFLINE, NO AGENTS, NO WIFI, CONNECTING (and the bridge's address), or NO BRIDGE ON THIS NETWORK, asking whether the Mac is on it; there, PWR shows device info. A card for an agent with no prompt on screen says NOTHING NEEDS YOU, or WAITING - ANSWER AT KEYBOARD.
- Device info says why the pager last started (POWER ON, RESTART, CRASH, BROWNOUT or RESET), its address and the address it dials, and NO SOUND if its audio failed at start-up. WIFI *name* FAILED after a network refuses it three times in a row. NO DEVICE KEY, with the way out, on a pager whose key can't be used.
- The setup page, `pager-bridge doctor`, the Homebrew formula, the release, the Claude Code plugin and the guides (above).
- `pager-bridge --version`. The firmware's version and the bridge's come from one file, `bridge/internal/version/VERSION`; a build that isn't a published version says `-dev`.
- CI on every push and pull request: the bridge's tests and the firmware in the emulator against every golden screen on macOS arm64, and the firmware's host tests in the pinned ESP-IDF container. Every action is pinned by its commit SHA; Dependabot watches the actions and the Go modules.

### Changed (since v0.1.0)

- **The bridge's Go module is `github.com/MaverickHQ/herdr-pager/bridge`,** so `go install` works.
- The firmware takes lcd349-kit from its public repository, at `v0.2.2`, and the component lock files are committed, pinning the kit's commit and every other component.
- ESP-IDF v5.5.6, the kit's, in place of v5.5.5: for the board, the emulator, the host tests and CI.
- A refused Wi-Fi join is tried again at once, three times in a row.
- Holding PWR 2 s toggles silent mode only on the list and on cards. Interrupt checks the agent again when you choose it and when you confirm.
- Pairing is two messages (ADR 0011): an offer with nothing secret, which the pager shows and BOOT confirms; then, once you've confirmed on the page or answered `y` to `pager-bridge provision`'s question on the terminal, the Wi-Fi networks. `provision --yes` skips only the question, for unattended tests. A pager with v0.1.0's firmware refuses the offer, and v0.1.0's bridge can't pair this firmware: update both together.
- Go 1.26.10, pinned in `go.mod`'s `toolchain` line.
- `golang.org/x/crypto` at the commit that fixes GO-2026-5932 (its `openpgp` package, which the bridge never builds in; no tagged release has the fix yet), and `golang.org/x/sys` v0.49.0.

### Fixed (since v0.1.0)

- A full event queue never drops a button press or release.
- A card opened during a page's 3 s wait answers that page.
- The stored brightness is applied at boot.
- A setting whose store fails is still drawn and applied.
- Pairing erases the network slots the new list doesn't use.
- herdr's `agent_not_ready` answer counts as a stale prompt: nothing is sent.

### Known problems

- **macOS on Apple Silicon only,** and **Claude Code's keymap only:** any other agent shows on the list and can be jumped to, but its card offers no approve or deny.
- **The pager's storage isn't encrypted.** Whoever holds it, or any computer it's plugged into, can read its key and Wi-Fi passwords.
- **On battery, it only says ON BATTERY:** it doesn't show the battery's level, save power or turn itself off.
- **Back home, it stays on a phone's hotspot** while the hotspot is up, even when the Mac has left it.
- **A link that dies quietly** (the Mac asleep, or gone from the network) is noticed within 8 s of a press, but otherwise only after about 150 s.
- **A few restarts at the desk had no recorded cause.** Device info now says why the pager last started, so the next one will.
- **The bridge isn't signed with an Apple Developer ID.** Homebrew and curl don't quarantine what they download, so macOS runs it; `SHA256SUMS` and the build attestations vouch for it.
- Flashing from the setup page needs Chrome or Edge (WebSerial).

## v1.0.0-rc.1 (2026-11-08)

A release candidate for v1.0.0, published to rehearse the release from end to end: CI's archive and libssh's source, their build attestations, the Homebrew formula in `MaverickHQ/homebrew-tap`, and `brew install`. It holds what v1.0.0's entry below lists. Try it if you like; v1.0.0 follows it, and replaces it in the tap.

