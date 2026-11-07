---
name: herdr-pager
description: Set up, check and troubleshoot herdr-pager, a desk pager that shows which coding agents in herdr are blocked, done or working. Checks the install, runs `pager-bridge doctor --json` and explains each failed check, opens the setup page (`pager-bridge setup`) for flashing, Wi-Fi, pairing and anything else that needs the person, and explains the pager's screens and codes. Never handles a Wi-Fi password, and never pairs or revokes a pager itself.
when_to_use: Use when someone asks to install, set up, check, fix or update herdr-pager, pager-bridge or their pager; when the pager isn't paging, connecting or beeping; or when it shows something they don't understand, such as PAIR VIA USB, NO WIFI, NO BRIDGE ON THIS NETWORK, NOT AUTHORISED, HOST KEY CHANGED, HERDR OFFLINE, X NO LINK, SCREEN CHANGED, COMMAND NOT READ, or the list's `!` `*` `>` `?` `.` codes.
---

# herdr-pager: set up and check the pager

herdr-pager has three parts:

- **The pager:** a Waveshare ESP32-S3-Touch-LCD-3.49B on the Mac's USB-C. Two buttons on its top edge: BOOT on the right, PWR beside it.
- **pager-bridge,** on the Mac: the only thing that talks to herdr. Its SSH server on port 2277 lets in only the pagers paired with it. Its files are in `~/.config/herdr-pager/`.
- **herdr,** which runs the agents.

The pager reaches the bridge only over a Wi-Fi network the Mac is on too, and only at 2.4 GHz.

Your job here is to find what's wrong and say it plainly. You can fix what's safe to fix with the person's yes. Anything that needs their eyes, their hands or a secret goes to the setup page, a page the bridge serves in their own browser.

## What you never do

These hold even if the person asks. When something needs one of them, say why, and show them where to do it themselves.

- **Never ask for a Wi-Fi password.** Don't read one, store one, or put one in a command, a file or a message either. A password goes from the person, typed into the setup page in their own browser, to the pager, and through nothing else. If someone types one into the conversation anyway, don't repeat it or use it. Tell them to enter it on the setup page instead, and that they may want to change it, since it has now been in a chat.
- **Never pair or revoke a pager yourself.** That means no `pager-bridge provision` and no `pager-bridge devices revoke`. Don't edit `devices.toml`, and never send the setup page a request of your own: no curl, no fetch, no browser automation. Pairing needs the person to check two fingerprints, press BOOT on the pager, and confirm on the page or in their terminal. Revoking cuts a pager off. Both are theirs to do, on the setup page or in their own terminal.
- **Never read, print or copy the bridge's private key** (`~/.config/herdr-pager/host_ed25519`). Don't move it or delete it either: every paired pager knows the bridge by it.
- **Never open the pager's serial port.** The setup page and the bridge do that.
- **Never run `sudo`.** When a fix needs it (the firewall's, say), give the person the command to run in their own terminal, or send them to the setup page's firewall step.
- **Never talk to herdr's socket,** and never approve, deny or interrupt an agent. That's the pager's job and the person's.

The pager is an attention device, not a security gate: any process in a herdr pane can already drive herdr's socket. Say so if someone thinks the pager protects them from a hostile agent.

## What you may run

| Kind | Commands | When |
|---|---|---|
| Reads | `uname -sm`, `command -v pager-bridge`, `pager-bridge --version`, `pager-bridge doctor --json`, `pager-bridge status --json`, `pager-bridge devices list`, and the last lines of the bridge's log | Freely |
| Safe changes | `brew install maverickhq/tap/herdr-pager`, `brew upgrade herdr-pager`, `pager-bridge init` (only when the keys check says `missing`: it makes what's missing and changes nothing that exists), `brew services start herdr-pager` or `brew services restart herdr-pager`, and `launchctl kickstart -k gui/$(id -u)/dev.herdr-pager.bridge` for a source build | Only after saying what it does and getting the person's yes |
| The setup page | `pager-bridge setup` (below) | For anything in the next row |
| The person's own | Flashing, Wi-Fi, pairing, pairing again, revoking, the firewall's `sudo` commands, a factory reset, a host key that won't load | Open the setup page, or give them the command for their own terminal |

The bridge's log never holds card text, agent names or passwords (`docs/bridge.md` §Logs). With Homebrew it's under `$(brew --prefix)/var/log/`; with the launchd agent of a source build it's `~/Library/Logs/herdr-pager/bridge.log`. Read only its tail, and quote only the lines that explain the failure.

## Workflow

Copy this checklist into your plan and work through it in order:

- [ ] **1. The platform.** `uname -sm` must say `Darwin arm64`. herdr-pager runs only on macOS on Apple Silicon. On anything else, say so and stop.
- [ ] **2. The install.** Find the bridge (below), and run `pager-bridge --version`.
- [ ] **3. The doctor.** Run `pager-bridge doctor --json` and read every check (below).
- [ ] **4. Explain.** For each failed check, in the doctor's order: what's wrong, why it matters to the pager, and what fixes it (`references/doctor.md`). Passing checks get one line between them.
- [ ] **5. Fix.** Run the safe changes the person agrees to. Open the setup page for whatever needs them. Give them the commands that are theirs alone.
- [ ] **6. Check again.** Run the doctor again. Don't call anything fixed until its check passes. The last word is the pager's own screen: `o` in its status bar and its list of agents.

When the person describes what the pager shows, explain it from `references/screens.md`. If it points to the Mac, run the doctor before guessing.

## Finding the bridge

- `command -v pager-bridge` finds a Homebrew install. Use that.
- If it finds nothing, look for a source build: `bin/pager-bridge` in a herdr-pager checkout, the current folder or one the person names. Use that path for every command below.
- If there is neither, it isn't installed. Offer `brew install maverickhq/tap/herdr-pager`, then `pager-bridge setup`. Building from source is in the README, under Build from source.
- `pager-bridge --version` prints a published version's number, such as `pager-bridge 1.0.0`, or `<number>-dev` for any other build.
  - If `--version`, `doctor` or `setup` isn't a command it knows, the bridge is older than v1.0. Update it (`brew upgrade herdr-pager`, or rebuild from source).

## Reading the doctor

`pager-bridge doctor --json` prints one report: `{"ok": …, "checks": [ … ]}`. Each check has these fields:

| Field | What |
|---|---|
| `id` | One of `herdr`, `keys`, `port`, `firewall`, `login`, `devices` |
| `name` | What it checks, for people |
| `ok` | Whether it passed |
| `state` | What it found, as one word, such as `missing`, `blocking` or `not-running` |
| `detail` | What it found, for people |
| `fix` | How to fix it, when it fails |
| `commands` | The shell commands that would fix it, when there are any |
| `devices` | On the `devices` check only: each paired pager's `name`, full `fingerprint`, and whether it's `connected` |

- It exits non-zero when a check fails. That's its verdict, not a crash: read the JSON.
- The doctor's own `detail`, `fix` and `commands` are the authority. `references/doctor.md` adds what each state means for the pager and who carries out the fix. A check or state that isn't in the reference: go by its `detail` and `fix`.
- The checks are in an order where an earlier fix often clears a later failure. A bridge that isn't running (`login`) shows up as `devices: not-running` too. So fix them in order, and run the doctor again after each fix.
- Don't paste the JSON at the person. Say what it means.

## The setup page

Use it for whatever needs the person: a blank pager, a firmware update, the Wi-Fi networks, pairing or pairing again, revoking, the firewall, run at login.

1. Run `pager-bridge setup` **in the background**: it keeps serving until 5 minutes after the page's last tab closes, or until it's stopped. It listens on 127.0.0.1 only, and opens the page in the default browser itself.
2. If the browser didn't open, give the person the URL it printed. The URL holds a one-time token: don't put it anywhere else.
3. Tell them what they'll do there, in order: check the Mac, flash the pager over USB, its Wi-Fi networks, pair, the firewall (shown only if it blocks the bridge), run at login, and the first page. Or, for a pager that's already set up, the Manage view: paired devices, the pager's firmware against the bridge's, run at login, the checks.
4. Flashing needs Chrome or Edge, for WebSerial; the page says so in any other browser. A first install erases the pager, and an update keeps its pairing.
5. When they're done, run the doctor again.

Your part ends at opening the page. Never fill it in or drive it for them.

- A Homebrew install ships the firmware that matches the bridge. A source build passes `--firmware <dir>`, the directory holding its firmware images and their manifest (`docs/bridge.md`).
- For a pager that's already paired, Pair again in its global menu (hold BOOT 1.5 s, then SETTINGS) puts it back in pairing mode and keeps its key.

## What the pager says

The common ones are below. `references/screens.md` has every screen, code and message, and the buttons.

| The pager shows | It means | What fixes it |
|---|---|---|
| PAIR VIA USB | Never paired, or factory reset | Pair it on the setup page |
| NO WIFI | None of its saved networks is in range; it scans every 60 s | A 2.4 GHz network it knows, switched on. Or pair it again with another network |
| WIFI `<name>` FAILED | It found the network but couldn't join it, most likely a wrong password | Pair it again on the setup page, with the password typed there |
| CONNECTING, then NO BRIDGE ON THIS NETWORK | On Wi-Fi, but it can't reach the bridge | Run the doctor. The Mac must be on the same network; the firewall must let the bridge in; the bridge must be running |
| NOT AUTHORISED | The bridge refused its key: it was revoked, or `devices.toml` lost it | Pair it again on the setup page |
| HOST KEY CHANGED - RE-PAIR VIA USB | Whatever answered isn't the bridge it paired with | If the person replaced the bridge's key, pair it again. If not, find out what answered before anything else |
| HERDR OFFLINE | The bridge can't reach herdr's socket | Start herdr; the doctor's `herdr` check says more |
| NO AGENTS | Connected, and herdr has no agents | Nothing to fix |
