# Design Document

## Overview

herdr-pager has two programs joined by one SSH connection:

```
  Waveshare ESP32-S3-Touch-LCD-3.49B                         Host (macOS, runs herdr)
 ┌────────────────────────────────────┐                ┌──────────────────────────────────────────┐
 │ pager firmware (ESP-IDF, C/C++)    │                │ pager-bridge serve (Go, one binary)      │
 │                                    │   SSH :2277    │                                          │
 │  buttons ─┐        ┌─ LVGL UI      │  device DIALS  │  sshserver ── session ── watch ──┐       │
 │  touch ───┼─ app ──┤               │ ─────────────► │  (full-key    (verb       (polls │       │
 │  timers ──┘ loop   ├─ audio        │  NDJSON over   │   allowlist)   allowlist)  1 Hz) │       │
 │                    └─ net/ssh_link │  exec channel  │                      │           ▼       │
 │  NVS: Wi-Fi, host key, device key  │                │                card + keymap   herdr     │
 └────────────────────────────────────┘                │                      └──────►  .sock     │
                                                       │                     (JSON, 0600, local)  │
        USB-C (pairing only) ─────────────────────────►│ pager-bridge provision                   │
                                                       └──────────────────────────────────────────┘
```

- **The firmware is a thin terminal.** It renders what the bridge sends, turns button presses into verbs, and decides locally when to beep: debounce, reminders, snooze, quiet hours.
- **The bridge holds all knowledge about herdr:**
  - which methods to call;
  - which keys each agent needs;
  - how to sanitize terminal text;
  - how to check that a verdict still applies.

  If herdr ever ships a request-bound `agent.respond` (herdr discussion #4462), only the bridge changes.

Evidence for the choices below: `reports/ESP32 agent pager prior art.md` (the report) and the recon in requirements.md §Assumptions.

## Key Decisions

| ID | Decision | Choice | Rationale |
|---|---|---|---|
| D1 | Transport and auth | Device is an SSH **client**. The bridge embeds an SSH **server** (`golang.org/x/crypto/ssh`) on `:2277` that accepts only full public keys listed in `devices.toml`. | Satisfies "device dials out" and "full keys, no custom crypto". Recon showed macOS Remote Login is off, and enabling it would expose the system `sshd`, which allows password auth by default, just for a pager. This matches herdr's own remote model (everything over SSH) without granting the device herdr's whole API. |
| D2 | Approval semantics | An approval is a keystroke sequence from a per-agent keymap, sent only after the bridge re-reads the agent and confirms `agent_status == blocked`, the same `state_change_seq`, and the same card hash. | herdr has no typed approve. `agent.send_keys` success means "bytes reached the PTY", not "request resolved" (#4462). The re-check binds the press to what the screen showed, following herdr-web-ui's approval cards that "are checked to be current before your answer is sent". |
| D3 | Change detection | Poll `agent.list` once per second and diff. Polling is the source of truth. | Status events are subscribed pane by pane, and herdr 0.8.0 was observed dropping them (herdr-assist reconciles every 1 s; herdr-remote polls every 2 s). One local socket call per second is negligible (N4). |
| D4 | Firmware stack | **Decided by phase 0's spike (2026-10-21).** Plain ESP-IDF with **`david-cermak/libssh` 0.12.2** (an ESP-IDF component, LGPL-2.1 as a separate library, ADR 0004) and an **ed25519 device key**. **No arduino-esp32.** Also LVGL 9 (through lcd349-kit), `esp_lcd` and `esp_codec_dev`; no `mdns` component, because lwip's one-shot mDNS query resolves `.local` names (amended 2026-10-22, phase 2's grilling). Only `net/ssh_link.*` may include libssh headers. | Plain ESP-IDF gives control of `sdkconfig` for auto light-sleep (tickless idle), Wi-Fi power save and, later, flash encryption. The spike's first library passed every check (`spikes/ssh_esp/RESULTS.md`): handshakes of 0.49–0.72 s, a link that costs about 21 KB of internal RAM, a 30-minute hold with −24 B drift, and a silent bridge noticed in 89 s. The same `link_libssh.c` also ran against the Mac's Homebrew libssh, so the emulator's `net/ssh_link` can be the same code. Isolating SSH in one module keeps a later swap to one module. |
| D5 | Security posture | The pager is an attention and control device, not a security gate. | Every herdr pane gets `HERDR_SOCKET_PATH`, and the socket checks only file ownership, so a prompt-injected agent can already send keys itself. The design's job is that a pager can do no more than a person pressing one key at the keyboard (R13). |
| D6 | Reach on the go | The device dials the bridge on whatever saved network it has joined: home or office Wi-Fi, or a phone hotspot the host has also joined, if the phase 3 hotspot spike passes. **No tailnet** (ADR 0005). | A tailnet would only add the case of a Mac left at home with the owner away and without a laptop; the owner's phone covers that better. Its cost (encryption and secure boot on the device, an immature client needing about 116 KB of internal RAM, a 3 s heartbeat that defeats light sleep, a cloud account) outweighs that one case. |
| D8 | Danger flags | The bridge matches the full, untruncated, unsanitized command against data-driven rules (`risk.toml`; sanitizing is for display only, ADR 0006). The flag rides in the card and its hash; the device adds a banner, a tone and a 2 s hold. | The bridge sees the whole command before truncation; the device doesn't. Rules as data can be reviewed, tested and overridden. |
| D9 | Hardware boundary | All hardware access goes through the lcd349-kit board SDK, a separate Apache-2.0 project. The pager builds for the board and for the kit's emulator from the same sources. | A second project will target the same board, so the board layer, the emulator, the MCP server and the Claude skill are reusable products in their own right. This also keeps the unlicensed Waveshare demo code out of both projects. |
| D7 | Touch versus buttons | Touch navigates; buttons commit. Approve needs a 1 s BOOT hold; the press that wakes the screen does nothing else; touch never wakes the screen. | The screen is a capacitive strip about 84 × 23 mm (pending the ruler, X-03) whose touch the pager reads one point at a time, and the device will live in a pocket. Touch makes browsing fast. Physical holds make every decision deliberate and work blind, with gloves, and when touch is cut (cut list 2). |

**D4 decided (P-01: grilled 2026-10-18, spiked 2026-10-21; ADR 0004).** Phase 0's spike tried the libraries in this order:
1. `david-cermak/libssh` 0.12.2, a plain ESP-IDF component with no Arduino: **passed, and chosen;**
2. LibSSH-ESP32 with arduino-esp32: not needed;
3. libssh2 with P-256 keys: not needed.

wolfSSH was ruled out (GPLv3). arduino-esp32 isn't part of the stack.

### Rejected Alternatives (deviations that break the architecture)

- **D1 — OpenSSH forced command** (`command=… ,restrict` in `authorized_keys`) as the MVP transport. Rejected for this host because it needs Remote Login. Kept as a roadmap option: `pager-bridge stdio` (R11.3) is exactly the program such a line would run.
- **D1 — Device connects straight to `herdr.sock`** over SSH stream-local forwarding (the Heeler pattern). Rejected: a device credential would grant `pane.send_text` and `agent.prompt`, so a lost pager would effectively be a shell. ESP32 support for `direct-streamlocal@openssh.com` is also unverified.
- **D1 — Anthropic's BLE "buddy" protocol, a WebSocket/MQTT relay, or a LAN TCP token** (herdr-assist). Rejected:
  - each uses link-layer bonding or a shared secret instead of public keys;
  - several make the device the listener;
  - the buddy protocol speaks to the Claude desktop app, not herdr.
- **D2 — Device-signed approvals** (signature over a command hash). Rejected: it is a bespoke signing protocol (custom crypto) whose signature herdr never checks (D5).
- **D2 — Queuing verdicts across disconnects.** Rejected: a stale "yes" could land on a different prompt. Nothing is queued or replayed.
- **D3 — `events.subscribe` as the primary source.** Rejected for the MVP (per-pane subscriptions, observed drops). Roadmap optimisation only.
- **D4 — Pure Arduino core.** Rejected: no tickless-idle or auto light-sleep control.
- **D4 — MicroPython.** Rejected: no SSH client library.
- **D4 — Rust (esp-hal).** Rejected: no mature SSH client.
- **D4 — wolfSSH as a fallback.** Rejected 2026-10-18 (ADR 0004): GPLv3 conflicts with publishing the firmware under Apache-2.0; the registry version carried CVE-2026-16516; there is no ESP-IDF client example.
- **D6 — Cloud relay** (claude-code-remote, Pairfob). Rejected: a third party in the path, and accounts (non-goal).
- **D6 — Phone app relaying over BLE.** Rejected: it means building and maintaining a phone app.
- **D6 — Port forwarding :2277 on the home router.** Rejected: it exposes the bridge to the whole internet and doesn't help when the host is a laptop on the move.
- **D6 — a tailnet on the device** (MicroLink, or a plain WireGuard client). Rejected 2026-10-18 (ADR 0005).
- **D6 — Probing the hotspot subnet for the bridge.** Rejected: fragile, and it blurs the meaning of a host-key mismatch (R3.3).
- **D7 — Touch approve, including slide-to-approve.** Rejected: pocket and palm touches, no tactile confirmation, and R8.2.
- **D7 — Touch-to-wake on V2 boards.** Rejected: in a pocket it would keep the screen and touch scanning on.
- **D8 — Danger detection on the device.** Rejected: the device only sees truncated, sanitized text.
- **D8 — Asking an LLM to classify risk.** Rejected: non-deterministic, it sends prompt text to another service, and it suggests a gate the pager isn't.
- **D9 — A pager-only simulator inside this repo.** Rejected: it couldn't be reused by the second project.

## Architecture

### Repository layout

```
.kiro/specs/herdr-pager/        requirements.md, design.md, tasks.md
bridge/                         Go module "github.com/MaverickHQ/herdr-pager/bridge" (renamed for publishing, 2026-11-03, phase 5 task 1.1)
  cmd/pager-bridge/             one file per subcommand: init, serve, stdio, status, provision, devices; stdlib `flag`, no cobra (amended 2026-10-22, phase 2's grilling)
  cmd/fakeherdr/                a runnable fake herdr (scenario file or `--churn`), for the emulator harness, N4 and N6 (amended 2026-10-22, phase 2's grilling)
  internal/config/              ~/.config/herdr-pager/config.toml, paths, modes
  internal/devices/             devices.toml load/add/revoke (atomic, 0600), full-key matching
  internal/herdr/               Unix-socket JSON client: agent.list/get/read/send_keys/focus, and the read-only tab.list and workspace.list, only (amended 2026-10-24, the desk checkpoint)
  internal/herdrtest/           fake herdr socket server for tests (scripted from fixtures); `cmd/fakeherdr` wraps it
  internal/watch/               1 Hz poller, diff, `since` tracking, broadcast latest snapshot
  internal/sanitize/            terminal text → safe ASCII display text
  internal/keymap/              embedded default keymaps + user overrides, validation
  internal/card/                detection read → summary/lines/kind/hash
  internal/protocol/            pager protocol v1 types, NDJSON codec with size limits
  internal/session/             per-device state machine, verb allowlist, stale checks, rate limit
  internal/sshserver/           x/crypto/ssh server, auth callback, channel/request policy
  internal/bridge/              wires the watcher, the sessions and the SSH server once, for `serve`, `stdio` and the end-to-end tests (amended 2026-10-27, the owner's decision, open-items O-36)
  internal/clock/               the one clock interface the watcher, sessions and server run on, and the real clock (amended 2026-10-27, open-items O-36)
  internal/provision/           USB-serial pairing client
  internal/devsim/              device simulator (SSH client + protocol) for integration tests
  internal/risk/                danger rules (embedded `risk.toml`, kept here so go:embed reaches it, + user overrides), matcher (amended 2026-10-22, phase 2's grilling)
  internal/stats/               episode timeline (JSONL, 0600, rotated) and the stats report
  keymaps/<kind>.toml           one file per agent kind, embedded by `keymaps/embed.go` (package keymaps) (R23)
  emutest/                      the pager's emulator harness (S2-7; ADR 0007): drives the kit's `lcd349` CLI, a real `serve` and `fakeherdr`
  testdata/herdr/<kind>/        fixtures captured from the real herdr (task 0.4), with `manifest.toml` classifying each one
  testdata/herdr/socket/        herdr 0.9.3's socket shapes for the seven methods, from its own API schema (not captured live)
  testdata/protocol/            a valid and an invalid example of every pager protocol message (phase 2's contract)
  testdata/provisioning/        a valid and an invalid example of every `@P` provisioning line (phase 2's contract)
firmware/                       ESP-IDF project; depends on lcd349-kit (idf_component.yml: pinned by commit until the kit's `v0.1.0`, then by tag (amended 2026-10-22, phase 2's grilling); the tag `v0.1.0` since phase 2's task 10.1)
  (no main/board/)              the lcd349-kit SDK owns power hold, buttons, backlight, LCD, touch, battery, RTC, expander, Wi-Fi and audio
  main/platform/                the glue: app_main (boot order), the event pump (kit events → app events; it retries a refused Wi-Fi join), the action executor, settings in NVS, fonts and theme (amended 2026-10-22, phase 2's grilling)
  main/net/                     ssh_link (ONLY place with the SSH library; resolves `.local` through getaddrinfo), its TCP connection and its line assembler
  main/app/                     the app core: codec (NDJSON), model, alert engine, input router, menus, link backoff, pairing, fingerprints; later the gesture recogniser (pure C, host-testable)
  main/ui/                      LVGL screens
  main/prov/                    the device key: made on first use, kept in NVS
  main/testhooks/               the test build's scripted presses (`@T`) and log lines (`@L`); empty in a release build (phase 2 D2-4)
  test_host/                    Unity host tests for main/app and ssh_link's address and line code (phase 2 D2-2) (amended 2026-10-22, phase 2's grilling)
  scenarios/                    golden screenshots for the emulator harness (`bridge/emutest`, R19; ADR 0007)
web/                            ESP Web Tools page and manifest (R22), published per release
contrib/launchd/                dev.herdr-pager.bridge.plist
docs/                           pairing guide, on-the-go guide (hotspot settings), hardware notes, power measurements
```

### Bridge components and interfaces (Go)

```go
// internal/herdr — one request per connection, 2 s timeout, method allowlist enforced here.
// Fields as herdr 0.9.3 sends them (amended 2026-10-22, phase 2's grilling); optional ones are omitted, never null.
type Client struct{ SocketPath string }
type AgentInfo struct {
    TerminalID      string  `json:"terminal_id"`             // stable when a pane moves: the bridge's key for an agent
    PaneID          string  `json:"pane_id"`                 // "w1:p3": the target for every call; changes if the pane moves
    WorkspaceID     string  `json:"workspace_id"`
    TabID           string  `json:"tab_id"`
    Agent           *string `json:"agent"`                   // the kind, e.g. "claude": the keymap's key
    Name            *string `json:"name"`                    // only for agents started with agent.start or renamed
    TitleStripped   *string `json:"terminal_title_stripped"` // the title; `title` and `display_agent` exist but only metadata hooks set them
    Status          string  `json:"agent_status"`            // idle|working|blocked|done|unknown
    StateChangeSeq  uint64  `json:"state_change_seq"`        // one counter shared by all agents; per-agent equality still holds
    CompletionSeq   *uint64 `json:"completion_seq"`          // which completion: present on done, and on idle after a focus
    Focused         bool    `json:"focused"`
    Revision        uint64  `json:"revision"`
}
func (c *Client) ListAgents(ctx context.Context) ([]AgentInfo, error)
func (c *Client) GetAgent(ctx context.Context, pane string) (AgentInfo, error)
func (c *Client) ReadDetection(ctx context.Context, pane string) (text string, err error) // source "detection", format "text"
func (c *Client) SendKeys(ctx context.Context, pane string, keys []string) error
func (c *Client) Focus(ctx context.Context, pane string) error
```

- **herdr 0.9.3's socket** (protocol 22; `bridge/testdata/herdr/socket/`, from herdr's own API schema) (amended 2026-10-22, phase 2's grilling):
  - One request per connection: one JSON line ending in `\n` (without it herdr waits 5 s and closes without answering), one line back, then herdr closes. herdr sets no deadline of its own for these methods, so the client's 2 s is the only bound.
  - Request `{"id":"<string>","method":…,"params":…}`; `params` is always sent, `{}` when empty. Success `{"id","result":{"type":…}}`; error `{"id","error":{"code","message"}}`. The client checks `error` first, then that `result.type` is the one its method expects: `agent_list` (`agents`), `agent_info` (`agent`, for get and focus), `pane_read` (`read.text`), `ok` (send_keys), `tab_list` (`tabs`) and `workspace_list` (`workspaces`) (the last two amended 2026-10-24, the desk checkpoint).
  - Errors are matched by `code`, never by message: `agent_not_found` → `unknown_pane`; `agent_not_ready` (the pane's foreground program is no longer the agent) → `stale`, logged `why=agent_not_ready` (phase 2 built `herdr_error`; `stale` since 2026-10-27, the owner's decision, open-items O-46); `invalid_key` → `herdr_error`, logged loudly (a keymap bug); `agent_send_keys_failed`, `invalid_request`, `internal_error` → `herdr_error`; `server_unavailable`, a refused or missing socket, and the 2 s timeout → `herdr_offline`.
  - A code is logged as herdr sent it only if it has a code's shape, lower case and underscores, at most 32 bytes; any other is logged as `other`, and answers `herdr_error`. A shape rather than a list, so a code a later herdr adds still shows (amended 2026-10-27, the owner's decision, open-items O-19).
  - A `send_keys` success means the keys were queued to the pane, not that the prompt was answered.
  - There is no protocol-version check: `ping` isn't one of the allowed methods (R13.2). An `agent.list` that won't decode is a herdr error, shown and logged with its code.

- **`watch`:** one goroutine.
  - Every second (fixed; there's no `poll_interval` setting, (amended 2026-10-22, phase 2's grilling)) it calls `ListAgents`, then `ListTabs` and `ListWorkspaces` for the names (amended 2026-10-24), builds `[]protocol.Agent`, and records `since` (bridge time when an agent's `state_change_seq` was first seen).
  - Agents are keyed by `terminal_id`, and addressed by their current `pane_id`. The display name is the agent's herdr tab as herdr's tab bar shows it, `<workspace number>.<tab number> <tab label>` (for example `4.7 C-Claude-Code-Herdr`, from `tab.list` and `workspace.list`), else `name` when herdr has one, else the terminal title without Claude Code's status glyph, else `<kind> <pane id>` (for example `claude w1:p3`); cut at its end at a word, never in the middle (amended 2026-10-24, the desk checkpoint). Beside it the list shows where the agent works, its folder's name (`at`).
  - When the digest changes, it publishes to subscribers through a latest-value slot (capacity 1, overwrite), so a slow session never blocks the poller.
  - On error it publishes `herdr: "offline"`.
- **`card.Build(info AgentInfo, detection string, km keymap.Map) Card`** (amended 2026-10-22, phase 2's grilling) (matching works on the raw prompt, sanitizing comes last: ADR 0006):
  1. Cut the prompt box out of the raw detection text: the lines below the keymap's `box_start` (the last horizontal rule above the prompt). The rest of the screen (old conversation, the launch line) is never matched or shown.
  2. `kind` = `permission` if a `permission_match` regex matches the raw box; else `question` if blocked; else `status`. Anchoring to the box means an old "Do you want to proceed?" in the scrollback can't make a question a permission card.
  3. `command_match` (shell prompts), `file_match` (edit, create and Read prompts) `url_match` (fetch prompts) or `mcp_match` (MCP calls: the arguments, with the server and tool) extracts the command, path, URL or arguments from the raw box, the head; the danger rules run on it (D8). The line the head was read from (`Read(<path>)`, `url: <url>`, the argument lines) isn't shown again (amended 2026-11-01 and 2026-11-02, open-items O-88 and O-90).
  4. Drop menu lines (`^\s*>?\s*\d+[.)]\s`), hint lines (`esc to cancel` and the like), and lines matching the keymap's `drop_lines` (tips, rules) and `ignore_match`.
  5. Summary = the box's title line (for example "Bash command", "Edit file"). Lines = the command or path first, then up to 3 lines in all; an MCP card, the server and tool, then one line per argument, up to 4 lines in all, the protocol's most (amended 2026-11-02, O-90's review).
  6. Sanitize each display field last (§Sanitizer), then wrap and truncate to 40.
  7. `hash` = SHA-256 hex of the canonical JSON `{pane, seq, kind, summary, lines}` (plus `risk` and `rl` on a danger card), then `raw`, the command, path or URL as step 3 extracted it, in full and unsanitized, so a change that truncation hides still changes the hash (amended 2026-11-01, the security pass).

  **The head's kind** (the security pass's review, 2026-11-01; amended the same day, open-items O-88): the box's title line decides first whether it's a file prompt (`file_prompt`) or else a fetch prompt (`url_prompt`) or an MCP call (`mcp_prompt`), and only that kind's pattern reads the head: `file_match` on a file prompt, `url_match` on a fetch prompt, `mcp_match` on an MCP call and `command_match` on any other; each is anchored to the box's start, so neither a description, a file's content, a fetch's prompt text nor an MCP call's arguments can stand in for another's head. `mcp_match` reads two groups, the server and tool from the line under the title (it must end `(MCP)`) and the arguments, one ` <name>: <value>` line each between the two rules; an argument that wraps, or any other line there, reads none. An MCP card shows the server and tool first, whole, then each argument on a line of its own, its name whole and a long value cut in the middle (the owner's decision, 2026-11-02); a tool longer than a line or holding hidden characters, more than 3 arguments, or a name leaving its value fewer than 10 characters fails closed. The danger rules read each argument's value from inside its opening quote, raw and with its quote characters and its \n, \t, \" and \' escapes as spaces (§Danger rules), and the hash covers the tool and the arguments raw. The tool is named as its server describes it: a server the owner doesn't trust could name one misleadingly (O-90, 2026-11-02, and its review). A pattern may read either of two layouts, one group in each (`file_match`: an edit's or a create's path, or a Read's); the bridge takes the first group that matched. Claude Code's `command_match`, its Read layout of `file_match`, and its `url_match` also need the box to hold exactly its two runs of `╌`. `url_match` reads two groups, the host the prompt names (Claude Code's own parse of the URL) and the URL on its line; the bridge takes the URL only if it parses as an http or https URL with no user name, whose host, in ASCII, is that host, and if the URL's line and the line under it are each the only line of the box that starts as it does, up to its `: ` (` url: `, ` prompt: `), so a URL that the screen breaks before a line looking like the next field, moving its host or hiding its rest, reads as none. A fetch card's second line is that host, bare, in place of Claude Code's "Claude wants to fetch content from" line, so a host made to start like a known one shows whole; a host longer than a line fails closed. Claude Code's `file_match` reads its first layout only under the "Edit file" and "Create file" titles (amended 2026-11-01, O-88's review).

  **Fail closed** (the owner's decision, 2026-11-01; the security pass, open-items O-80): a permission card whose head step 3 can't extract (a `╌` in a command stops `command_match`), whose head holds what sanitizing hides without a trace (an escape, a control character other than newline and tab, DEL, a bidi or zero-width character: even a terminated escape string can hide a payload), or whose box the keymap's `approve_option` doesn't match (approve presses option 1; the pattern confirms option 1 is the approve, reading only the real option list after the box's last `╌` rule: nothing after its `1. Yes` may hold a `╌`, so a decoy between two rules can't stand in, O-88's review), offers `deny` and `jump` only. Its first line is `COMMAND NOT READ`, `FILE NOT READ` when the keymap's `file_prompt` matches the box, `URL NOT READ` when its `url_prompt` does (O-88), or `TOOL NOT READ` when its `mcp_prompt` does (O-90), then an MCP call's tool and the head if they were read, then the box's other lines. Claude Code's prompts the bridge reads (O-88 and O-90, Claude Code 2.1.316): a Bash command, a file edit or create, a Read outside the folder, a web fetch and an MCP tool call (captured in a session without `--safe-mode`, which loads no MCP server, in the recon's own sign-in folder); any prompt of a kind not listed fails closed or is a question. With nothing extracted, the danger rules run on the whole raw box, and the line holding a hit's fragment follows the first line. The bridge re-checks on a verdict: a verb the rebuilt card doesn't offer gets `not_allowed`, and nothing is sent. The pager offers deny there, and BOOT's cell says to approve at the keyboard.

  The function is pure, so re-validation calls it again and compares hashes.
- **`session`:** handles one device connection.
  - States `await_hello` (5 s timeout) → `active` → `closed`.
  - Commands are handled one at a time per session, and a per-pane mutex across sessions prevents double sends.
  - Enforces R13.1 (verb allowlist, five-strike close), R13.5 (512-byte lines) and R13.6 (10 messages/s token bucket).
- **`sshserver`:**
  - `ServerConfig{NoClientAuth: false, MaxAuthTries: 3, PublicKeyCallback: devices.Match}`, with no password or keyboard-interactive callbacks.
  - It accepts only `session` channels, and only `exec` or `shell` requests on them; the command string is ignored. It rejects `pty-req`, `env`, `subsystem`, `x11-req` and all global or forwarding requests.
  - Each channel's stdin/stdout is wired to a `session`.
  - It stats `devices.toml` every 500 ms and, when it changed (as after `devices revoke`), re-reads it and closes sessions whose key is no longer listed, within 2 s (amended 2026-10-22, phase 2's grilling). A `devices.toml` that won't load (only a hand edit can break it: `provision` and `devices revoke` write it atomically) pairs no device until it loads again: every session closes and no key authenticates, it's logged at Error, and `status` says why (`devices_error` in `status.json`). It fails closed, as `serve`'s start does, so a broken edit can't keep a revoked pager paired (the owner's decision, 2026-10-27, open-items O-51).
  - Global requests are refused with reply false, never a disconnect: libssh's keepalive (`keepalive@openssh.com`, want-reply) counts a reply-false as alive (phase 0's spike) (amended 2026-10-22, phase 2's grilling). Algorithms are x/crypto's defaults.
  - A new session from the same key closes the old one, whether it comes on a new connection or as another session channel on the same one. A connection ends with its newest session, and the server returns only once every session has ended (amended 2026-10-27, the owner's decision, open-items O-34).
  - A connection has 15 s on the server's clock from its accept to its session's `exec` (or `shell`); one still in its handshake then, or authenticated and quiet, is closed and logged (`sshserver: no session within 15 s; connection closed`, with the source address only). Connections still in their handshake are closed when serve stops, so one that stalls can't hold `Serve` (parent 3.9; the owner's decision, 2026-10-27, open-items O-47).
  - At most 32 connections are in their handshake at once, and at most 4 from one source address; one more is closed at its accept, unless all 32 are taken and the address holding the most holds more than one more than the newcomer's: then that address's oldest connection is closed and the newcomer takes its slot (T11). A temporary `Accept` failure (EMFILE, ENFILE and the like) is tried again after a backoff of 5 ms doubling to 1 s, so a flood can't make `serve` exit. The lines an unauthenticated client can cause (a refused key, no session within 15 s, a connection over the cap, an `Accept` failure) are each logged at most once a second, the next saying how many were held back (`held_back`) (amended 2026-11-01, the security pass, open-items O-80).
  - Idle timeout is 180 s without any device message (the device pings every 60 s).
- **`serve` ↔ the CLI** (amended 2026-10-22, phase 2's grilling): `serve` writes `status.json` (0600, atomic rename, every second and on change) in the config directory: listening, the herdr state, devices and sessions. `status` and `devices list` read it; a missing file, or one older than 5 s, means "not running". There's no control socket.

### Pager protocol v1 (NDJSON over the SSH exec channel)

Framing and limits:
- One JSON object per line, terminated by `\n`. Every message has a type field `t`.
- Device requests carry a numeric `id`, which the bridge echoes in `result`, `card` and `pong`.
- Device → bridge lines are at most 512 bytes; the bridge closes the session on violation.
- Bridge → device lines are at most 8,192 bytes; the device drops a longer line.
- Neither limit counts the `\n` (amended 2026-10-22, phase 2's grilling).
- `pane` is herdr's full pane id (`w1:p3`), never a short form (amended 2026-10-22, phase 2's grilling). Each of its ids is ASCII digits and letters, since herdr counts past 9 with letters (`w6:pE`, `wA:p1`; amended 2026-10-24, found live at the desk checkpoint).
- Snapshots carry at most 16 agents, sorted by priority, with `more` counting the rest.
- The device's hello may carry `lines`, 3 or 4, the most lines of a card it draws; without it the bridge takes 3. The bridge sends a card of 4 lines (an MCP call's tool and 3 arguments) only to a pager whose hello says 4; for any other, such a card fails closed in 3 lines (TOOL NOT READ). `v` stays 1: a bridge from before refuses any other, and ignores `lines`, so a new pager still works with it (amended 2026-11-02, O-90's second review). Update the pager and the bridge together all the same.

Device → bridge (the complete allowlist):

```json
{"t":"hello","v":1,"fw":"0.1.0","name":"pager-01","lines":4}
{"t":"ping","id":7}
{"t":"list","id":8}
{"t":"show","id":9,"pane":"w1:p3","pg":4200}
{"t":"approve","id":10,"pane":"w1:p3","seq":41,"hash":"<64 hex>"}
{"t":"deny","id":11,"pane":"w1:p3","seq":41,"hash":"<64 hex>"}
{"t":"interrupt","id":12,"pane":"w1:p3","seq":40}
{"t":"jump","id":13,"pane":"w1:p3"}
```

Bridge → device:

```json
{"t":"hello","v":1,"bridge":"0.1.0","host":"studio-mac","now":1760220000}
{"t":"snap","now":1760220001,"herdr":"ok","more":0,
 "agents":[{"pane":"w1:p3","name":"api-refactor","kind":"claude","st":"blocked",
            "seq":41,"dseq":7,"since":1760219900,"can":["show","jump"]}]}
{"t":"snap","now":1760220002,"herdr":"offline","more":0,"agents":[]}
{"t":"card","id":9,"pane":"w1:p3","st":"blocked","seq":41,"hash":"<64 hex>","kind":"permission",
 "summary":"Bash command","lines":["npm test -- --watch=false","in ~/src/api"],"can":["approve","deny","jump"]}
{"t":"card","id":14,"pane":"w1:p5","st":"blocked","seq":9,"hash":"<64 hex>","kind":"permission",
 "risk":"danger","rl":"FORCE PUSH","summary":"Bash command","lines":["git push --force origin main"],
 "can":["approve","deny","jump"]}
{"t":"result","id":10,"ok":true}
{"t":"result","id":10,"ok":false,"err":"stale"}
{"t":"pong","id":7,"now":1760220060}
```

- `err` is one of `stale`, `not_allowed`, `unknown_pane`, `herdr_offline`, `herdr_error`, `bad_request` or `rate_limited`. `bad_request` is reserved: an unknown or invalid message gets no reply (parent 13.1; the owner's decision, 2026-10-27, open-items O-52).
- `pg` in `show` is the milliseconds since this agent's page fired, or −1 if it never paged. It feeds the stats timeline (R21) and nothing else.
- `risk` and `rl` appear only on danger cards and are part of the hashed card text (R18.5).
- `dseq` is herdr's `completion_seq` when it's present (it says which completion), else 0; whether the agent is done comes from `st` (amended 2026-10-22, phase 2's grilling).
- A snapshot entry may carry `at`, where the agent works: its folder's name, 0–16 printable ASCII. Absent or empty, the device shows the kind there (amended 2026-10-24, the desk checkpoint).
- `can` offers `interrupt` only for a working agent, and a question card offers only `jump` (amended 2026-10-22, phase 2's grilling).
- Snapshot `can` never contains approve or deny. Only a card can offer them, and only when `kind == "permission"` and the agent kind has a keymap (R7.7, R8.6).
- A permission card that failed closed (§Bridge components, `card.Build`) offers `deny` and `jump` without `approve`; an approve on it gets `not_allowed` (amended 2026-11-01, the security pass).

### Verdict handling (bridge)

```
on approve|deny {pane, seq, hash}:
  verb allowed by allowlist?                    else → not_allowed
  km := keymap[agent kind]                      missing → not_allowed
  lock(pane)
  info := herdr.GetAgent(pane)                  error → herdr_error / unknown_pane
  if info.Status != "blocked" || info.StateChangeSeq != seq   → stale
  card := card.Build(info, herdr.ReadDetection(pane), km)
  if card.Kind != "permission" || card.Hash != hash           → stale
  herdr.SendKeys(pane, km.Keys(verb))           error → herdr_error
  → ok
  unlock(pane)

on interrupt {pane, seq}: same, but requires Status == working, the same seq, and km.Interrupt non-empty.
  (Amended 2026-10-22, phase 2's grilling: not on blocked, where `esc` is a deny that would skip the card's stale check.)
on jump {pane}: herdr.Focus(pane).
```

There is a small window between the re-read and `send_keys`; it is accepted (see Threat model, T6).

If herdr ever offers a request-bound respond method (R20), `SendKeys(km.Keys(verb))` becomes that call. The stale checks stay.

### Danger rules (`internal/risk`)

```toml
[[rule]]
label   = "FORCE PUSH"
pattern = '''\bgit(\s+(-C\s+\S+|-c\s+\S+|--git-dir=\S+|--work-tree=\S+))*\s+push\b.*\s(--force(-with-lease)?|-f)\b'''
tests_match    = ["git push --force origin main", "git push -f", "git -C work push --force"]
tests_no_match = ["git push origin main", "git push --follow-tags"]
```

- Rules are matched against the full, raw command that `command_match` extracted (or the path from `file_match`, the URL from `url_match`, or an MCP call's arguments from `mcp_match`, O-90), before sanitizing and truncation (ADR 0006): each rule, in order, on the raw text and on a copy of the same length with its quote characters and its `\n`, `\t`, `\"` and `\'` escapes as spaces, the first rule matching either winning, its fragment from whichever it matched. So a rule bounded by a space or the text's start or end fires inside quotes (`bash -c "sudo …"`), and a rule that reads quotes itself (the git rules' quoted `-C`, `-c`, `--git-dir` and `--work-tree` values) still fires on the raw text (amended 2026-11-02, O-90's reviews). The whole box of a card whose head wasn't read is matched the same way.
- Since a Read outside the folder and a fetch can be approved from the pager (open-items O-88), SECRETS flags reading or writing a file that holds a secret (the bridge's own host key under `~/.config/herdr-pager` among them: a copy lets anyone pose as the bridge, T2), and LOCAL ADDRESS a fetch whose URL starts with a local address (localhost, loopback, the cloud metadata address, the private ranges); a command that only names one isn't flagged (amended 2026-11-01, O-88's review).
- Rules for git allow its global options (`-C dir`, `-c k=v`, `--git-dir=…`, `--work-tree=…`) between `git` and the subcommand; phase 0's danger fixture (`git -C work push --force`) is one of the cases (amended 2026-10-22, phase 2's grilling).
- Sensitive-path rules (R18.7: `.env`, `~/.ssh` and the like) apply to the path of an edit or create prompt too.
- Every rule carries its own test cases. A test runs them all, and `serve` refuses to start on a user rule whose tests fail.
- **Truncation keeps the evidence:** when a danger fragment would fall in the truncated middle, the line becomes `head .. fragment .. tail`, within the 40-column limit.

### Stats (`internal/stats`)

- One JSONL line per event: `{ts, pane_hash, seq, ev, ms?, end?}`. `pane_hash` is an HMAC of the pane id with a per-install key, so lines can't be linked to herdr panes after the fact. No text, names or keys are stored.
- `stats` computes the R21.2 figures. "Page to verdict" uses `pg` plus the time between `show` and the verdict.

### Keymaps

Embedded `keymaps/<kind>.toml` files (here `claude.toml`), overridable by `~/.config/herdr-pager/keymaps.toml`:

```toml
[claude]                       # key = AgentInfo.agent (task 0.4 confirms the string)
approve   = ["1"]              # task 0.4: becomes ["1","enter"] if "1" only moves the selection
deny      = ["esc"]
interrupt = ["esc"]            # never ctrl+c: it can exit Claude Code
permission_match = []          # filled from captured detection fixtures (task 0.4); matched against the prompt box only
ignore_match     = []          # lines that change while blocked (keeps the hash stable)
box_start        = ''          # the rule above the prompt: the box is the lines below its last match (amended 2026-10-22)
drop_lines       = []          # tips and rules inside the box that never reach a card (amended 2026-10-22)
file_match       = ''          # the path in an edit or create prompt (amended 2026-10-22)
file_prompt      = ''          # matches a file prompt's box: an unread path says FILE NOT READ (amended 2026-11-01)
url_prompt       = ''          # matches a fetch prompt's box: an unread URL says URL NOT READ (amended 2026-11-01, O-88)
url_match        = ''          # a fetch prompt's named host (group 1) and URL (group 2); the URL must be of that host (O-88)
mcp_prompt       = ''          # matches an MCP call's box: an unread call says TOOL NOT READ (amended 2026-11-02, O-90)
mcp_match        = ''          # an MCP call's server and tool (group 1) and argument lines (group 2) (O-90)
approve_option   = ''          # matches the box only when approve's keys pick the approve; without it, no approve (amended 2026-11-01)
question_match   = ''          # a line asking the question: approve needs exactly one in the box (amended 2026-11-02, O-90's review)
```

- Each kind lives in its own file, `keymaps/<kind>.toml`, with `agent_version` and `captured` fields and a `command_match` regex that extracts the requested command from the detection text (R18.1, R23).
- Validation: every key must match `^(esc|enter|tab|up|down|left|right|backspace|[0-9]|[a-z]|ctrl\+[a-z])$`; every regex must compile; an agent kind with an empty `permission_match` gets no approve or deny, and one with no `approve_option` gets no approve (amended 2026-11-01).
- Invalid user overrides stop `serve` with an error naming the file and key. Bad config never silently falls back.

### Sanitizer (`internal/sanitize`)

It runs last, on each display field a card or snapshot carries; matching never sees its output (ADR 0006, (amended 2026-10-22, phase 2's grilling)).

The order matters:

1. Strip CSI/OSC/DCS escape sequences. This is defence in depth: herdr already strips ANSI. A control string (OSC, DCS, SOS, PM, APC) with no terminator loses only its introducer, and in decoded text a C1 code point begins no sequence (step 2 removes it as one control), so neither can hide the rest of a field (amended 2026-11-01, the security pass).
2. Remove C0 and C1 controls except `\n`.
3. Remove bidi embeddings, overrides and isolates (U+202A–U+202E, U+2066–U+2069) and zero-width characters (U+200B–U+200D, U+FEFF, U+2060).
4. Transliterate `❯ › » → >`, `… → ..`, `– — → -`, `‘ ’ → '`, `“ ” → "`, and box-drawing characters to `|`, `-` or `+`. Replace any other non-ASCII rune with `?`.
5. Fold runs of whitespace to one space per line.
6. Truncate head-and-tail with `..`. Names are limited to 24 characters, kinds to 10, folders (`at`) to 16, summary and lines to 40. Names and folders are cut at their end instead, at a word where one ends in the second half, then `..` (amended 2026-10-24, the desk checkpoint).

The firmware also replaces any byte outside 0x20–0x7E with `?` before rendering.

### Firmware components

**Boot order** (`app_main`):
1. I2C bus.
2. TCA9554, then assert P6 power-hold. This must happen before anything slow (R10.1).
3. Backlight PWM.
4. LCD and LVGL.
5. Buttons.
6. NVS settings.
7. Either provisioning mode (no config) or Wi-Fi → `ssh_link` (which resolves the host, `.local` included, through getaddrinfo).
8. Audio.
9. Start `app_loop`.

**Concurrency:** a single event queue feeds `app_loop` (core 1), which owns the model. There are no locks in app logic.

| Task | Core | Role |
|---|---|---|
| `app_loop` | 1 | Consumes `BUTTON{id, kind}`, `NET{msg}`, `LINK{state}`, `TICK{1 s}` and `PROV{msg}`; updates the model; calls UI functions; enqueues net sends and tones. |
| `ui` | 1 | Runs `lv_timer_handler()` only while the backlight is on. When it's off, it blocks on a semaphore so the CPU can light-sleep. |
| `net` | 0 | Wi-Fi events, `ssh_link` connect/auth/read/write, NDJSON framing, reconnect backoff (1, 2, 4, 8, 16, 30 s, ±20 % jitter; at the 30 s cap the jitter only shortens the wait, to 24–30 s, so N3's 35 s holds after a long outage: the owner's decision, 2026-10-27, open-items O-48). |
| `audio` | 0 | Plays tone patterns from a queue (I2S → ES8311 → amp). |

**Buttons:** BOOT = GPIO0 and PWR = GPIO16, both active-low with pull-ups. The kit reports debounced edges with timestamps; the app core classifies them itself (amended 2026-10-22, phase 2's grilling: so its hold timings are tested on a fake clock, not taken on trust from the kit's gestures):

| Gesture | BOOT | PWR | Both |
|---|---|---|---|
| short (< 600 ms) | `BOOT_SHORT` | `PWR_SHORT` | — |
| held 1 s (emitted while still held) | `BOOT_HOLD1` | — | — |
| hold ≥ 1.5 s | `BOOT_HOLD` | — | — |
| held 2 s (emitted while still held) | `BOOT_HOLD2` | — | — |
| held 2 s (emitted once, while still held; amended 2026-10-22) | — | `PWR_HOLD2` | — |
| hold ≥ 5 s | — | `PWR_HOLD5` (countdown; release cancels; power off at 8 s) | — |
| hold ≥ 10 s | — | — | `CHORD_RESET` (single-button holds suppressed, R9.7). Both count as the chord only if the second button goes down before any hold threshold of the first; otherwise the first gesture stands and the second press is consumed (amended 2026-10-22). |

A hold is judged by the edges' own timestamps. The kit hands an edge over at least 10 ms after it happened, so a timer fires a threshold only 30 ms after it passes: a release stamped before the threshold, still on its way, wins. A release stamped at or after it fires the threshold at once. So every hold acts up to 30 ms later than its nominal time (the owner's decision, 2026-10-27, open-items O-22).

**Input router:** a context-to-action table (pure function `route(context, event) → action`, host-tested):

| Context | BOOT_SHORT | BOOT_HOLD1 | PWR_SHORT | BOOT_HOLD | PWR_HOLD2 | PWR_HOLD5 |
|---|---|---|---|---|---|---|
| ASLEEP (backlight off) | wake only | wake only | wake only | wake only | wake only | countdown |
| LIST | next row | — | open card (`show`); with the link down, "X NO LINK" and nothing sent; on an empty list with the link down, device info (O-77) | agent menu (or global menu if list empty) | toggle silent | power-off countdown |
| CARD_PERMISSION (shown ≥ 500 ms) | back to list | **approve** (rest of the press is consumed) | **deny** | — | toggle silent | countdown |
| CARD_DANGER (shown ≥ 500 ms) | back to list | fill bar continues; `BOOT_HOLD2` → **approve** | **deny** | — | toggle silent | countdown |
| CARD_PERMISSION (< 500 ms) | ignored | ignored | ignored | — | toggle silent | countdown |
| CARD_OTHER (question/status) | back to list | — | back to list | agent menu | toggle silent | countdown |
| MENU | next item | — | select | close menu | — | countdown |
| CONFIRM (interrupt, factory reset, pair) | NO (interrupt) / YES (reset, pair) | — | YES (interrupt) / NO (reset, pair) | — | — | — |
| INFO (device info; added 2026-10-27) | back | — | back | — | — | countdown |
| PROVISION_WAIT | — | — | — | — | — | countdown |
| ERROR (host key changed) | — | — | info | global menu | — | countdown |

- **Approve is a 1 s hold; BOOT short-press on a permission card is "back".** A reflexive tap, or a press in a pocket, can't approve. Deny stays a short press because it fails safe: Claude Code cancels the tool call and asks what to do instead.
- **The agent menu isn't reachable from a permission card by button.** Go back (BOOT short-press), then hold BOOT on the list row.
- **Interrupt confirm is inverted on purpose.** PWR=YES there, while approve is BOOT. A reflexive BOOT press from a card can never interrupt.
- **Interrupt checks again, at its select and at YES:** the agent must still be working, still offer `interrupt`, and still be at the seq it had when its menu opened. Otherwise the menu or question closes, SCREEN CHANGED shows, and nothing is sent (R9.5; the owner's decision, 2026-10-27, open-items O-27).
- **ASLEEP swallows the waking press** (R10.7), so at least three deliberate inputs separate a sleeping pager from any verdict: wake, open, then a 1 s hold.
- **The card guard:** a press that starts within 500 ms of a card appearing is ignored, its release included (amended 2026-10-22).
- **`PWR_HOLD2` toggles silent only where the table says so:** the list and the cards. In menus, device info, confirmations and the error screens it does nothing and stores nothing (the owner's decision, 2026-10-27, open-items O-23).
- **The factory-reset chord works on every screen, PROVISION_WAIT and the pairing confirmation included** (R2.2's "while the firmware is running"; the owner's decision, 2026-10-27, open-items O-28). FACTORY RESET? is drawn over the pairing screen. On a confirmation, the chord answers the `prov_set` `cancelled`; CANCEL goes back to PAIR VIA USB, and a `prov_set` that arrives while the question is up is answered `busy`. It's the way out for a pager whose stored key can't be read.

**Touch** (the kit's touch input plus a pure gesture recogniser in `main/app/gesture.c`, host-tested; the pager has no board layer, D9):
- **Hardware:** capacitive, built into the AXS15231B, I2C 0x3B. The kit polls it every 30 ms, only while the screen is awake and lit. There's no usable touch interrupt on either board revision: the expander's interrupt line fires on every panel refresh (P-03, 2026-10-20). Touch never wakes the screen (R15.1). Touches land 6–26 px below where the finger aims, which layouts allow for.
- **Geometry:** 7.62 px per mm by Waveshare's drawing (84.00 × 22.58 mm; 7.5 px per mm if the ruler says 86 mm, X-03), so the 172 px height fits at most two or three comfortable touch targets stacked vertically, while the 640 px width fits about nine side by side. Layouts therefore put touch targets side by side, and gestures run along the long axis.
- **Single point:** the controller reports two points; the recogniser uses the first only. Gestures are tap (< 300 ms, < 12 px of movement), long press (≥ 1 s, < 12 px) and swipe (≥ 40 px, dominant axis wins).
- **Mapping** (`TOUCH{gesture, x, y}` events go through the same router):

| Context | Tap | Long press | Swipe left/right | Swipe up/down |
|---|---|---|---|---|
| LIST | select row; tap the selected row → open card | agent menu for that row | — | move one screen |
| CARD_PERMISSION | — | — | previous/next card in list order | down → back to list |
| CARD_OTHER | — | — | previous/next card | down → back to list |
| MENU | select tile | — | scroll tiles | — |
| CONFIRM, PROVISION_WAIT, ERROR, ASLEEP | ignored | ignored | ignored | ignored |

- **List rows are 36 px (about 5 mm), below the 56 px target size.** That's acceptable because a mis-tap only moves the selection; opening needs a second tap on the highlighted row.
- **Touch can never emit a verdict, a YES or power off.** The router enforces it, and a host test proves it for every context.
- **Touch is ignored for 300 ms after wake** (R15.7). A touch also resets the 60 s card timeout (R7.6).

**Model** (`main/app/model.c`):
- An agents array of up to 16 entries, plus `more`.
- `acked` set of `(pane, seq)`, `seen_done` set of `(pane, dseq)`, `paged` set of `(pane, seq)`. Each is a ring of 64 entries.
- Snooze map: `pane → until`.
- Link state and herdr state.
- Settings mirror.

Sort order: `blocked`, then `done` with an unseen `dseq`, then `unknown`, `working`, `idle`; ties go to the smallest `since`. A seen done agent sorts with idle and shows "done" dimmed (amended 2026-10-22).

**Alert engine** (pure, host-tested, driven by `TICK` and snapshots):

| Event | Condition | Output |
|---|---|---|
| blocked page | `st == blocked` continuously for ≥ 3 s with the same seq, `(pane, seq)` ∉ paged and ∉ acked (a card shown in the 3 s answers the page before it fires: the owner's decision, 2026-10-27, open-items O-21). Pages and reminders falling due together sound once (open-items O-20) | Tone `BLOCKED`; backlight on (≤ 60 s); 3 screen flashes; highlight row; add to paged |
| reminder | paged, not acked, not snoozed, still blocked with the same seq, 5 min since last | Tone `BLOCKED` |
| auto-clear | paged pane leaves blocked or changes seq | Cancel reminders and highlight |
| done | `dseq` increased since last snapshot | Tone `DONE` once |
| low battery | V < 3.5 V; latches until > 3.7 V | Tone `LOW_BATT` once |
| quiet / silent | Silent mode, or quiet hours active | Suppress tones and flashes; vibration per R14.2 (optional add-on, the portable pager) |

**Tone patterns** (square wave via I2S at 16 kHz, volume from settings):
- `BLOCKED` "knock-knock": 880 Hz 120 ms, 80 ms gap, 880 Hz 120 ms, 400 ms gap, then the pair again.
- `DONE` "ta-da": 660 Hz 150 ms, 990 Hz 300 ms.
- `LOW_BATT`: 440 Hz 400 ms, 330 Hz 600 ms.
- `CLICK` (button feedback, optional): 2 kHz 15 ms.
- `DANGER`: 1,200 Hz 80 ms, 60 ms gap, three times, played when a danger card opens (R18.4).

**UI** (LVGL 9, landscape 640×172 via software rotation; full frames only, because the panel takes no row address, flushed by the kit in 13 ms with strip rotation):

```
y   0–27   top band     status on the left: "!2 *1 >3   o  14:32"      Terminus 12×24, DSEG7 digits
                        hint cells on the right, one under each button: [PWR: OPEN][BOOT: NEXT]
y  28–171  body         4 list rows × 36 px (Terminus 16×32, 40 cols)
                        or a card: a danger banner first if flagged, summary (16×32), detail lines (12×24)
```

- **Hint placement** (amended 2026-10-22, phase 2's grilling, item 16): both usable buttons sit at the right-hand end of the top edge, BOOT rightmost (P-03), so the hints sit in one top band with the status: a cell under each button, in their physical order, PWR then BOOT. Each cell is about 77 px wide (the BOOT cell, which ends at the screen's edge, 72 px), so a hold's label takes two lines of the small font, each at most 72 px. The body grows to 144 px, which fits a danger card. The cells' x positions come from the kit's generated button positions (phase 1's task 1.4), never hand-copied once the kit is tagged. Rejected: swapping the bands (still a 108 px body), a bottom bar (labels 23 mm from the buttons), a right-hand column (170 px off every screen), no hints (unsafe, since a button's meaning depends on the screen).
- **Menus** are a horizontal strip of tiles, each at least 120 × 96 px, with the selected tile inverted. BOOT steps through them and touch can tap them, which suits the wide, short screen.
- **Approve fill bar:** while BOOT is held on a permission card, the BOOT hint cell fills left to right over 1 s (2 s on a danger card), then shows "SENT" (amended 2026-10-22).

- **Row format (40 columns):** `G NNNNNNNNNNNNNNNNNNNN KKKKKKKK AGE`. The third column is the agent's folder (`at`), else its kind (amended 2026-10-24, the desk checkpoint: phase 2 D2-15).
- **On a card,** the band's status names the card's agent in place of the counts (amended 2026-10-24, the desk checkpoint).
- **Glyphs:** `!` blocked, `*` done-unseen, `?` unknown, `>` working, `.` idle. The selected row is inverted.
- **Link glyph:** `o` connected, `~` connecting, `x` auth rejected, `#` host key changed.
- **Palette:** two pairs, chosen with COLOUR in the global menu and stored as `colour`: ORANGE, the default, near-black `#0A0605` on a deeper orange `#CC5A2A` (4.95:1), and GREEN, a backlit pager's `#0B1A0B` on `#6FBF5A` (7.98:1). Contrast is on RGB565 colours, above the kit's 4.5:1 rule. They are the owner's choice at phase 2's desk checkpoint (2026-10-24), which replaced the "Bravo" green LCD look (`#0F380F` on `#8BAC0F`) and then Claude's orange (`#141413` on `#D97757`). A screen flash inverts the palette three times at 150 ms.
- **Fonts:** Montserrat (OFL-1.1, LVGL's built-in) at 12, 14, 20 and 28 px, ASCII 0x20–0x7E only. So every string on screen is ASCII: "OK" and "X" for a verdict's result, "-" for a dash (amended 2026-10-22). The owner used it through the desk checkpoint and the acceptance day and kept it (the owner's decision, 2026-10-27, open-items O-50); the Terminus and DSEG7 sizes in the layout above are the original plan. Terminus and DSEG7 Classic remain the way to win back about 60 KB of flash if the portable pager runs short (the release image leaves about 80 KB of its partition free): that needs `lv_font_conv`, Terminus's files checked against ADR 0004 (some packagings list GPLv2 beside OFL-1.1), DSEG renamed in the converted copy (its Reserved Font Name), and every golden redrawn.
- **List hints:** the PWR cell says `OPEN` and the BOOT cell `NEXT`, each only when it does something: with one agent the BOOT cell is blank, and with none (an empty list, or no list yet) the PWR cell is blank, or `INFO` while the link is down (device info, open-items O-77), and the BOOT cell says `HOLD:` over `MENU`, as on the error screens (the owner's decision, 2026-10-27, open-items O-33).
- **Card hints:** a permission card's PWR cell says `DENY` and its BOOT cell `HOLD:` over `APPROVE` (a danger card's, `HOLD 2S:` over `APPROVE`); a menu's BOOT cell says `NEXT` over `HOLD: EXIT`. Each line fits the 72 px BOOT cell in the 12 px hint font (amended 2026-10-23: `HOLD: APPROVE` is 105 px there, so the card's `TAP: BACK` line gave way; BOOT short still goes back from a card, unlabelled); a question card's body says `QUESTION - ANSWER AT KEYBOARD`. A question or status card's PWR cell says `BACK` and its BOOT cell `HOLD:` over `MENU`; a status card with no text of its own says `NOTHING NEEDS YOU` (a blocked agent's, `WAITING - ANSWER AT KEYBOARD`), then the agent's state and its age, then its kind and folder (the owner's decision, 2026-11-01, open-items O-82). Confirm screens put each button's meaning in its own cell, so no string fixes a BOOT-first order (amended 2026-10-22). A permission card that failed closed (`can` offers deny without approve; its first line `COMMAND NOT READ`, `FILE NOT READ`, `URL NOT READ` or `TOOL NOT READ`, from the bridge) takes PWR short as DENY, BOOT short as back, PWR's 2 s hold as silent, and nothing else: no BOOT hold approves it, fills the bar or opens a menu. Its PWR cell says `DENY` and its BOOT cell `APPROVE:` over `KEYBOARD` (64 and 71 px in the hint font; `APPROVE AT` is 80) (amended 2026-11-01, the owner's fail-closed decision).
- **No kinetic scrolling.** Lists and tiles move in whole steps. The panel takes only full frames, so smooth scrolling would redraw the whole screen at every step, and stepping suits the 1990s look anyway.

**Power:**
- `esp_pm_configure({max 160 MHz, min 40 MHz, light_sleep_enable = true})` with `CONFIG_FREERTOS_USE_TICKLESS_IDLE=y`.
- Wi-Fi uses `WIFI_PS_MAX_MODEM` with `listen_interval = 3`.
- `gpio_wakeup_enable()` on GPIO0 and GPIO16 for low level.
- Backlight (LEDC on GPIO8) turns off after 30 s idle, or 60 s after an unacknowledged page.
- Battery: ADC1 channel 3, 12 dB attenuation, ×3 divider, 16-sample average every 60 s, mapped to 4 bars on a Li-Po curve.
- Power off releases TCA9554 P6. If the device is still alive 1 s later (USB power), it shows "USB POWER" and turns the screen off.
- **ON BATTERY** (the desk pager, phase 2 R1.1): the status bar's flag comes from the kit's `lcd349_power_source()` and its event (USB, battery or unknown; phase 1 adds them). Unknown shows no flag (amended 2026-10-22).

**SSH link** (`net/ssh_link`; `david-cermak/libssh` 0.12.2 on the board and the host's libssh in the emulator, see D4):
- Open the TCP socket yourself, with a short connect timeout, and hand it over with `ssh_options_set(SSH_OPTIONS_FD)`, `USER="pager"`. Then `ssh_connect`.
  - libssh's own connect can't succeed on ESP-IDF: the port's `NI_NUMERICHOST` makes `getnameinfo` fail.
  - libssh never closes a socket given this way, so the link closes it after `ssh_disconnect`, or each link leaks it.
  - Set `HOME` (any path), or `ssh_connect` fails with "Couldn't apply options".
  - The full list of the spike's carry-overs is in `spikes/ssh_esp/RESULTS.md`: `SSH_AGAIN` from reads, mbedTLS threading, a 20 KB task stack, keepalive timing and reconnect timing.
- Host key check: `ssh_get_server_publickey` must satisfy `ssh_key_cmp(pinned, server, SSH_KEY_CMP_PUBLIC) == 0`, otherwise the link goes to state `HOSTKEY_CHANGED` and never authenticates. The app core keeps HOST KEY CHANGED on screen and dials again every 5 minutes (a dial that reaches nothing waits 5 minutes more), clearing the state when the pinned key answers (amended 2026-11-01, the security pass: it was terminal until a power cycle).
- Authenticate with `ssh_userauth_publickey(device_key)`; on failure the link goes to `AUTH_REJECTED` and retries every 5 min.
- Open the channel with `ssh_channel_open_session` and `ssh_channel_request_exec("pager")`.
- Read loop: `ssh_channel_poll_timeout` (1 s) feeding a line assembler, which drops any line over 8,192 bytes, not counting the `\n`.
- Send a `ping` every 60 s. Reconnect after 150 s with no bytes received.
- A show, verdict or interrupt the bridge hasn't answered in 8 s also drops the link, which redials after its backoff: the app core decides it. A show asked for again counts from the first, and a verdict is discarded, never resent. The bridge's slowest answer is a verdict's three herdr calls of up to 2 s each (amended 2026-10-25, phase 2's acceptance day: with the Mac gone from the pager's network, a press did nothing for up to 150 s).
- The host resolves through getaddrinfo: on the board, lwip's one-shot mDNS query answers `.local` names (`CONFIG_LWIP_DNS_SUPPORT_MDNS_QUERIES`); in the emulator, the Mac's resolver does. There's no `mdns` component. Provisioning with an IP always works (amended 2026-10-22, phase 2's grilling; the board lane verifies it). There's no `last_ip` fallback: `.local` resolved across an iPhone hotspot on the acceptance day (R3), and with no tailnet (ADR 0005) the pager only ever dials the bridge on a network it shares with the Mac, where a name that never resolved left it no address to remember (cut: the owner's decision, 2026-10-27, open-items O-49).

**Wi-Fi** (the kit's station, started by `platform/settings.c`):
- Up to 4 saved networks in priority order (R16.1). While disconnected, the device runs a scan every 60 s and joins the highest-priority visible network (R16.2).
- A refused join is tried again after 1, 2, 4, 8 and 16 s, then every 30 s (`platform/pump.c`), since the kit alone tries again only at its next scan (amended 2026-10-24, phase 2's task 10.6).
- The status bar shows the network in use, or "NO WIFI". A link that is down shows the link glyph `~` and pages nothing new (R16.4).
- `ssh_link` isn't started while the kit reports no Wi-Fi: the emulator's sockets ignore its simulated Wi-Fi, so NO WIFI is decided in product code (amended 2026-10-22).

**Provisioning** (`main/prov`), over the board's USB-C serial console:
- Provisioning messages are lines prefixed `@P `; every other line is a log line, which the tool ignores.
- **Key generation** happens only while Wi-Fi is started or after `bootloader_random_enable()`, so the RNG is truly random (RECON, task 6.2). The pager uses the radio, through the kit: `prov.c` starts the kit's station if it isn't started, waits until the kit reports it scanning (its radio is on), makes the key, and stops the station again if it started it. With the radio off, it makes no key, and a later boot tries again. `bootloader_random_enable()` isn't used: it bypasses the kit (D9), and it isn't safe while anything else uses the ADC, which the kit's battery reading does. libssh seeds its generator once, at startup, before any entropy source is on, and ed25519 keys draw from it without a reseed, so `ssh_link_keygen` first makes a strong draw, which reseeds it from the RNG. An unpaired pager never starts its station otherwise (`settings_start_wifi` does nothing with no network saved). The board checks see the order on each first boot (amended 2026-10-26, after phase 2: the review's open item 1). It uses `ssh_pki_generate(SSH_KEYTYPE_ED25519)`: ed25519, verified by phase 0's spike, which generated the key in 49 ms after Wi-Fi started. libssh 0.12 marks this function deprecated, so use its replacement if there is one. The private key is stored as an NVS blob. It is never printed or sent. A key is made only when none is stored: one that's there but can't be read is kept, no key is made, and the pager shows NO DEVICE KEY until a factory reset (the owner's decision, 2026-10-27, open-items O-07). That holds whether it's paired or not. A paired pager keeps its pairing stored, but goes to the same screen: it dials nothing, sends no `prov_hello`, and refuses a `prov_set`. The factory-reset chord, which works there, is the way out.
- **Messages** (amended 2026-10-22, phase 2's grilling: the contract is `bridge/testdata/provisioning/`, a valid and an invalid example of every line, which the bridge's tests and the firmware's both read; a `@P` line is at most 512 bytes):
  - On entering pairing mode, and every 2 s while in it, the device writes `prov_hello {fw, pubkey, state}`. `provision` opens the port and then waits up to 120 s for it, telling the owner to choose **Pair again** now. So the reset that opening the port causes (macOS raises DTR and RTS) can't cancel pairing mode.
  - `prov_offer {name, host, port, host_key, host_name}` → the device validates lengths and that `host_key` parses, shows the confirmation screen (both full SHA256 fingerprints, wrapped across two lines), and waits 30 s for BOOT or PWR. After BOOT its `prov_hello` says `confirmed`; then, once the owner has confirmed on the page or the terminal too, `prov_set {host_key, wifi:[{ssid, pass}] (1–4, priority order)}`, which the device takes without a second press if `host_key` is the confirmed offer's and it comes within 30 s (amended 2026-11-07, ADR 0011: nothing secret reaches the pager before both fingerprints are confirmed).
  - The device then replies `prov_result {ok, err?}`. An ok is sent three times, and the console drains for 250 ms before the restart, so a cut line can't leave the pager paired and the bridge not. `provision` passes over a damaged line (not one JSON value, or too long) and reads the next whole one (amended 2026-10-24, the desk checkpoint).
  - The device key must be ed25519 (D4); `provision` refuses any other type with a precise error.
  - `--host` defaults to `<LocalHostName>.local`, from `scutil --get LocalHostName` (a fixed argument vector, which R13.3 allows), falling back to the host name.
  - Before it opens the port, `provision` warns of each `--wifi-ssid` this Mac hasn't saved but has saved under a near spelling (apostrophes, quotes, dashes, case, spaces), naming the Mac's. It reads the list from `networksetup -listpreferredwirelessnetworks` on en0, else en1: fixed argument vectors (amended 2026-10-25, phase 2's acceptance day: an iPhone's hotspot name has a curly apostrophe).
  - Neither side ever prints or logs a Wi-Fi password.
- **Accepted only when** the device is in provisioning mode: unprovisioned, after a factory reset, or after **Pair again**.

### Data models

`~/.config/herdr-pager/config.toml` (bridge):
```toml
listen        = "0.0.0.0:2277"
herdr_socket  = ""          # empty → $HERDR_SOCKET_PATH, else ~/.config/herdr/herdr.sock
idle_timeout  = "180s"
log_level     = "info"      # info never logs card/detection text or agent names
```

`~/.config/herdr-pager/devices.toml` (0600, atomic writes):
```toml
[[device]]
name  = "pager-01"
key   = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... pager-01"   # full authorized_keys line
added = 2026-10-11T22:00:00Z
```

Also in that directory: `host_ed25519` (0600) and `host_ed25519.pub` (0644), `keymaps.toml` (optional overrides), and `status.json` (0600), which `serve` keeps current for `status` and `devices list` (amended 2026-10-22). There's no `poll_interval`: herdr is polled every second.

Device NVS (namespace `pager`):
- Wi-Fi: `wifi_n` and `wifi0_ssid`/`wifi0_pass` … `wifi3_ssid`/`wifi3_pass` (priority order).
- Host: `host`, `port`, `host_key` (full OpenSSH public key line), `host_name`. (`last_ip0` … `last_ip3` were cut: the owner's decision, 2026-10-27, open-items O-49.)
- Identity: `name`, `dev_key` (private key blob).
- Settings: `silent`, `quiet_on`, `quiet_start`, `quiet_end`, `volume`, `brightness`, `colour` (the palette; amended 2026-10-24), `done_tone` (`0` turns the done tone off; amended 2026-10-25).
- Add-on: `vibe_gpio` (optional, the portable pager; −1 = absent).

## Error Handling

| Failure | Bridge behaviour | Device behaviour |
|---|---|---|
| herdr socket missing or refused | `snap{herdr:"offline"}`; retry every poll | "HERDR OFFLINE" banner; list frozen and greyed |
| Socket permission denied | Same, and log the path and errno once | Same |
| Unknown pane in a command | `result{err:"unknown_pane"}` | Toast "GONE"; back to list |
| Stale verdict | `result{err:"stale"}`, no keys sent | "SCREEN CHANGED" 2 s; re-request `show` |
| `send_keys` error | `result{err:"herdr_error"}` (or `stale` for `agent_not_ready`: the owner's decision, 2026-10-27, open-items O-46) | "X" toast; for `stale`, SCREEN CHANGED, and the card is asked for again |
| Device line > 512 B, invalid JSON ×5, or rate exceeded | Close session or drop, and log | Reconnects with backoff |
| Line > 8 KB from bridge | — | Drop the line and log it; request `list` |
| SSH auth rejected | Log the full fingerprint and source address | "NOT AUTHORISED"; retry every 5 min |
| Host key mismatch | — | "HOST KEY CHANGED - RE-PAIR VIA USB"; retry every 5 min, never authenticating to the changed key; cleared when the pinned key answers (amended 2026-11-01) |
| Wi-Fi join fails | — | "WIFI <ssid> FAILED"; try the next saved network; retry with backoff, except that a join the access point refuses is tried again at once, three refusals in a row (the owner's decision, 2026-10-27, open-items O-45); Pair again in menu |
| No saved network visible (for example, a hotspot left at 5 GHz) | — | "NO WIFI"; scan every 60 s; device info lists the saved networks and the 2.4 GHz note |
| On a network that can't reach the bridge | — | `~` link glyph and the network name; no new pages; the list shows "LAST SEEN <age>". An empty list says "NO BRIDGE ON THIS NETWORK" and asks whether the Mac is on it ("CONNECTING" until a dial there fails); PWR shows device info, which says why the pager last started and the address it dials (the owner's decision, 2026-10-27, open-items O-77) |
| mDNS resolution fails | — | Show `~` link; redial with backoff (no `last_ip` fallback: cut, the owner's decision, 2026-10-27, open-items O-49) |
| NVS config corrupt or partial | — | Enter provisioning mode (key kept if it is intact) |
| Audio init fails | — | Continue silent; device info shows "NO SOUND" |
| Invalid user keymap or config | `serve` exits non-zero, naming the file, field and reason | — |

## Threat model

Inputs and trust boundaries:

| Input | Source | Trust |
|---|---|---|
| Device messages | Over SSH | Authenticated but untrusted content |
| herdr agent data | Contains terminal text written by agents | Untrusted |
| USB provisioning lines | Physical access | Trusted only after on-device confirmation |
| `config.toml`, `keymaps.toml`, `devices.toml` | Host user | Trusted |

| # | Threat | Mitigation | Residual / accepted |
|---|---|---|---|
| T1 | LAN attacker connects to :2277 | Full-key allowlist; no password or keyboard-interactive auth; `MaxAuthTries 3`; maintained x/crypto/ssh | Pre-auth bugs in x/crypto/ssh. Keep it updated. |
| T2 | LAN man-in-the-middle of the device link | Full host key pinned at USB pairing (read from the local file, so no TOFU); on a mismatch the pager aborts before authenticating, shows HOST KEY CHANGED, and dials again only every 5 minutes, clearing the state when the pinned key answers | None beyond SSH itself for the link's secrecy. Availability: whoever can answer the pager's dial on the LAN (a spoofed mDNS reply, or a host on the bridge's address) can keep it on HOST KEY CHANGED, re-answering every 5 minutes, but never learns its key or a verdict; one spoofed reply costs at most 5 minutes (amended 2026-11-01, the security pass: the state was terminal until a power cycle) |
| T3 | Lost or stolen pager | Can only approve, deny, interrupt or jump; only on permission prompts it's shown; in the MVP only from a network that reaches the bridge; `devices revoke` takes effect within 2 s; a `devices.toml` that won't load pairs no device, so a broken hand edit can't keep a revoked pager paired (the owner's decision, 2026-10-27, open-items O-51) | **Accepted:** NVS is unencrypted, so the device key and Wi-Fi passwords can be extracted with physical access. `devices revoke` and changing the Wi-Fi password close it. Flash and NVS encryption are an optional hardening (Later), not required: the only credential that needed them, a tailnet key, is gone (ADR 0005). A computer the pager is plugged into over USB can read its flash the same way, so the docs say to plug it only into computers you trust, and after one you don't, to revoke it, factory reset it (a new key), pair it again and change the Wi-Fi passwords; flash and NVS encryption with secure download mode are backlog P-44, for the portable pager (amended 2026-11-01, the owner's decision in the security pass). |
| T14 | Accidental presses in a pocket or bag | The waking press does nothing (R10.7); touch never wakes the screen (R15.1); a card must be open ≥ 500 ms; approve needs a 1 s hold (R8.1) | A pocket can still deny, which fails safe |
| T4 | Prompt-injected agent approves its own prompt | Out of scope: the agent can already use `herdr.sock` without the pager (D5) | **Accepted.** A real gate belongs in the agent's permission layer (roadmap). |
| T5 | Terminal text spoofs what's being approved (bidi, escape sequences, homoglyphs) | Sanitizer: ASCII only, controls, bidi and zero-width stripped; the firmware filters again | An agent can still write misleading plain text, the same as at the keyboard |
| T6 | Verdict lands on a different prompt (race) | Re-read plus seq and hash check immediately before `send_keys`; per-pane lock; no queuing | A window of milliseconds between re-read and send. **Accepted** until herdr offers an atomic respond (#4462). |
| T7 | Question prompt answered as a permission | `permission_match`; question cards offer no verdicts | Detection patterns drift with agent versions; task 0.4 fixtures plus a test catch regressions |
| T8 | Device injects arbitrary keys or text | Verb allowlist; keys come only from the keymap; herdr method allowlist (R13.2) | None |
| T9 | Command injection via agent text | No subprocesses take untrusted data; herdr is reached only through the JSON socket (R13.3) | None |
| T10 | Pairing hijack | USB only, so physical access is needed; both full fingerprints shown on device and host; device button press required | An attacker with physical access to both machines (out of scope) |
| T11 | Device DoS against the bridge | 512-byte line cap, 10 messages/s, five-strike close, idle timeout; 15 s from accept to a session, so a stalled or quiet connection is closed (parent 3.9; the owner's decision, 2026-10-27, open-items O-47). Before authentication: at most 32 connections in their handshake and 4 from one address, any more closed at accept; when all 32 are taken, a newcomer takes the slot of the oldest connection of the address holding the most, if it holds more than one more than the newcomer's, so a pager (one slot) gets in past a few busy addresses and is never the one closed; a temporary `Accept` failure is retried, not fatal; the lines such clients cause are logged at most once a second (amended 2026-11-01, the security pass, open-items O-80) | Pre-auth flooding of :2277 from 32 or more addresses, each holding a slot and reconnecting every 15 s, can keep a pager's dial out (it retries with backoff). Fewer addresses can't. A LAN host can claim many addresses, so bind to a LAN interface or firewall the port if that matters. |
| T12 | Weak device key from unseeded RNG | Key generation only with RF on or `bootloader_random_enable()`; the pager uses the radio, and reseeds libssh's generator first (§Provisioning) | None once verified (task 6.2): `TestDeviceKeyIsMadeWithTheRadioOn` in the emulator, and the board checks' pairing on the board (2026-10-26). A key made before then may be weak: factory-reset that pager and pair it again |
| T16 | A danger rule is missed or bypassed (an obfuscated command, a new tool) | Rules are a reading aid, documented as such (R18.6). The card always shows the command first. A card whose command, path, URL or MCP call can't be read in full (a URL also when its host isn't the one the prompt names; an MCP call when its tool doesn't fit a line), or whose option 1 can't be confirmed as the approve, fails closed: COMMAND NOT READ (or FILE NOT READ, URL NOT READ or TOOL NOT READ) first, deny only, with the danger rules run over the whole raw box when nothing was read; so a command the card can't show isn't approvable from the pager (amended 2026-11-01 by the owner, open-items O-80) | **Accepted:** an unflagged card is not a safe card. A failed-closed card is approved at the keyboard |
| T17 | Stats file leaks activity patterns | No text, names or keys; HMAC'd pane ids; 0600; `--reset` | Timing patterns remain on the local disk |
| T13 | Supply chain | Few dependencies, each checked with valvur `check_package` before it's added; the bridge's `go.sum`; exact versions in the firmware's component manifests (libssh `==0.12.2`; the kit's LVGL, display driver and codec). The kit comes from its public URL at a tag, and the ESP-IDF component lock files (`dependencies.lock.*`) are committed: they pin the kit's commit, every transitive component's version and each component's hash (amended 2026-11-03, phase 5 task 1.2; until then they stayed local, since they recorded the kit's home-directory path: open-items O-53) | Normal residual risk: a published tag, and the registry's components, are trusted as their authors publish them |
| T18 | A web page or local process drives the setup page (CSRF, DNS rebinding, a log line carrying script) to revoke or pair a pager, or to read a Wi-Fi password | 127.0.0.1 only, a random port, a one-time token in the URL and on every request; Host and Origin checked; a strict content security policy, and log lines shown only as text; it runs only while `pager-bridge setup` runs and stops 5 minutes after its last tab closes; passwords are never stored, logged or echoed; pairing still needs BOOT on the pager; the page has no verdicts (ADR 0009, parent 22.5 and 22.6) | A local process running as the owner, which can already read `devices.toml` and drive `herdr.sock` |
| T19 | A tampered published version (a compromised build, tag or tap) | CI builds each version on a tag (never the Mac), with build attestations and `SHA256SUMS`; the formula pins each archive's sha256; actions pinned by SHA; tag and branch protection; the owner approves every push | **Accepted:** no Apple Developer ID, so the bridge is unsigned and macOS can't vouch for it; Homebrew's checksums and the attestations stand in. A compromised GitHub account defeats all of this |

## Testing Strategy

| Layer | What | How |
|---|---|---|
| Bridge unit | Sanitizer (ANSI, bidi, zero-width, transliteration, truncation), keymap validation, card builder and hash stability, protocol codec limits, devices store atomicity and full-key match, token bucket, session state machine | `go test ./...`, table-driven, fixtures in `testdata/` |
| Bridge integration | Real SSH on loopback: `devsim` (test device key) ↔ `serve` ↔ `herdrtest` fake socket | Scenarios: unknown key rejected; hello → snapshot ≤ 1 s; blocked → snapshot ≤ 1.5 s; approve sends exactly the keymap keys; stale by seq; stale by hash; question has no verdicts; revoke closes ≤ 2 s; herdr offline/online; oversize line; rate limit; verb allowlist; the fake herdr asserts no method outside R13.2 is ever called |
| Real-herdr smoke | `pager-bridge stdio` against the running herdr | Manual: a throwaway Claude Code pane hits a permission prompt (task 0.4 recipe); `show` and `approve` via stdio |
| Firmware host | `main/app` (model sort, alert engine with a fake clock, input router table including touch and pocket sequences, gesture recogniser, settings) and `net/ssh_link`'s address and line code | `firmware/test_host`: Unity on the `linux` target, run on the Mac (phase 2 D2-2; amended 2026-10-22) |
| Emulator (lcd349-kit) | Every screen's golden screenshot and UI lint; scripted scenarios for pairing over the virtual serial port, paging, cards (including danger), hold-to-approve, touch navigation and menus against a real bridge with `fakeherdr` | Headless, through the pager's own Go harness `bridge/emutest`, which drives the kit's `lcd349` CLI (ADR 0007); goldens in `firmware/scenarios/` (amended 2026-10-22) |
| Release | The setup page flashes and pairs a blank board; Homebrew install; N12 timed by a newcomer | Phase 5's checks (amended 2026-10-30) |
| On-device | Pairing, page tone, approve a real Claude Code prompt, stale scenario (answer at the keyboard first, then press BOOT), power off, light-sleep current, 24 h soak | Checklists in tasks.md with human checkpoints; measurements recorded in `docs/measurements.md` |

## Deployment and Operation

- **Bridge build:** `cd bridge && go build -o ../bin/pager-bridge ./cmd/pager-bridge`.
- **Bridge first run:** `bin/pager-bridge init`, then `bin/pager-bridge serve`. macOS may ask to allow incoming connections the first time; allow it.
- **Run at login:** `contrib/launchd/dev.herdr-pager.bridge.plist` installs to `~/Library/LaunchAgents/`; start it with `launchctl bootstrap gui/$UID <plist>`. Logs go to `~/Library/Logs/herdr-pager/bridge.log`.
- **Health:** `pager-bridge status --json` reports listening, herdr state, devices and sessions.
- **On a hotspot:** `docs/guides/hotspot.md` covers the phone hotspot settings (2.4 GHz), which setups the phase 3 hotspot spike proved, and the lost-pager steps: `devices revoke`, then change the Wi-Fi passwords the pager knew.
- **Firmware build:** `idf.py -C firmware set-target esp32s3 build flash monitor`. Emulator build and run: `lcd349 build -C firmware` then `lcd349 run -C firmware` (lcd349-kit CLI; headless with `--headless`).
- **Published versions** (amended 2026-10-30, ADRs 0009 and 0010): CI builds each one on a tag. Its GitHub release carries the firmware images, `pager-bridge` for macOS arm64 with the firmware images beside it, `SHA256SUMS`, build attestations, `THIRD_PARTY.md` and libssh 0.12.2's full source (LGPL-2.1, ADR 0004). The Homebrew formula installs the same archive; `pager-bridge setup` serves the setup page, which flashes the image that ships with it.
  - The ESP-IDF version range is in `firmware/main/idf_component.yml` (5.5; phase 2 built with v5.5.5), which also pins libssh to `==0.12.2` and the kit to its tag. The resolved versions are in `firmware/dependencies.lock.esp32s3` (`.linux` for the emulator), which stay local (`.gitignore`).
  - NVS survives reflashing unless `idf.py erase-flash` is run.
- **Rollback:**
  - Firmware: check out the previous tag and reflash.
  - Bridge: rebuild the previous tag. Configuration files are forward-compatible within v1.
  - Revoking a device is instant and needs no restart.
