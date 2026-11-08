# Design: Phase 2, the desk pager

## Overview

The parent design (`.kiro/specs/herdr-pager/design.md`) stands: the architecture, the protocol, the sanitizer, the danger rules, the input router tables and the threat model, as amended by this phase's grilling (2026-10-22: herdr 0.9.3's real shapes, the card pipeline, button hints at the top). This phase builds its desk subset in two halves that run in parallel:

```
Half A, the bridge (Go; no hardware, no kit)              Half B, the firmware (C on lcd349-kit, pinned by commit)
P-10 init/config/devices/provision                       P-15 project, kit wiring, platform glue, emulator harness
P-11 herdr client + watcher  ◄─ fake herdr (fixtures)     P-16 app core: codec, model, alerts, router (host tests)
P-12 sanitizer, keymap, risk, cards                      P-17 Wi-Fi, ssh_link, provisioning (in the emulator)
P-13 protocol + sessions + verdicts                      P-18 desk screens and goldens (real bridge + fake herdr)
P-14 SSH server, CLI, launchd, N4
                         └──── both halves meet in the emulator harness (S2-7) ────┘
                                              │
                         lane board: needs the kit's v0.1.0 and the board
                         (re-pin, esp32s3 build, ON BATTERY, P-19 on the board)
```

- Both halves start as soon as the foundation has laid down the contract. The firmware pins lcd349-kit by commit (`7c125c5`, the kit's `main` at this grilling) and builds for the kit's emulator; the kit's public API doesn't change in phase 1 (checked 2026-10-22), so nothing waits for its tag but the board work.
- **Lane `board`** waits for the kit's local `v0.1.0` (its `needs:`) and for the board (the runner holds back a lane with an open `[board]` task while no board is on USB). Its first tasks re-pin the firmware to `v0.1.0`; then it builds for the board and measures there.
- **So without the board,** a run does everything except lane `board` and the merge stage, then stops for the owner (exit 3) naming lane `board`.
- Within and across the halves, the work runs in parallel lanes (§Lanes): a foundation lays down the contract, ten lanes run side by side in their own worktrees, and a merge queue brings them back.

## Decisions (confirmed 2026-10-22, grilling items 11–19)

| # | Decision | Why |
|---|---|---|
| D2-1 | **The firmware logic is one deep module, the app core**: `app_handle(&state, event, now) → actions`. Events and actions are listed in S2-6. The codec, model, alert engine and router are its implementation, not separate seams. | One small interface carries every paging, safety and navigation rule. The tests survive internal restructuring. It's the natural home of the 10,000-run pocket fuzz test, and what the emulator harness drives through the real firmware. |
| D2-2 | Host tests for the firmware use **Unity** (from ESP-IDF), not vendored doctest | One dependency fewer; the kit's host tests already use it |
| D2-3 | **The bridge's main seam is end-to-end over real loopback SSH** (`devsim` ↔ `pager-bridge` ↔ the fake herdr) | It exercises auth, protocol, sessions, verdicts, stale checks and the method allowlist together; the internal packages stay free to change |
| D2-4 | Tests on the board use **test-build hooks only**: `@T press` button injection and timestamped logs. Release builds compile them out (`CONFIG_PAGER_TEST_HOOKS`). | Lets N1, N2, N3, N6 and N7 run unattended; a release build can't be driven by its serial port |
| D2-5 | **The kit pin.** Until the kit's local `v0.1.0` exists, the firmware pins lcd349-kit **by commit**: `git: file:///Users/<you>/lcd349-kit`, `path: components/lcd349`, `version: 7c125c5609b1772d3e88c523f05bb92865c64bd1`. Lane `board`'s first task re-pins it to the tag `v0.1.0`. Never `override_path`: the runner switches the kit's checkout to `phase-1` while phase 1 runs. Phase 5 switches to the public URL. | Nothing is published before the owner decides; a commit pin lets the firmware lanes run before phase 1 (proven 2026-10-22 with a scratch product: the component manager resolves a SHA in a local repo, and the lock file records no worktree path) |
| D2-6 | **Contract first.** Before any lane starts, the foundation fixes what the lanes share: the protocol fixtures (`testdata/protocol/`), the provisioning fixtures (`testdata/provisioning/`, D2-13), herdr's socket fixtures and the Claude fixture manifest (D2-11, D2-7), the bridge's file formats (`devices.toml`, `config.toml`, `status.json`, D2-12), and its package signatures with every dependency imported. Lane `fw` likewise fixes the firmware's file layout and the harness core before the other firmware lanes start. | Each side tests against the same fixtures rather than against the other's code. Lanes then stay inside their own files, and a mismatch shows up as a red test at a merge, not at the acceptance day. |
| D2-7 | **Cards match on raw text and sanitize for display last** (ADR 0006): detection text → the prompt box (below the keymap's `box_start`, the last horizontal rule) → `permission_match` and the question kinds on the raw box → `command_match` / `file_match` → the danger rules on the raw command or path → sanitize each display field. The summary is the box's title line; the keymap's `drop_lines` removes tips and rules. A fixture manifest (`bridge/testdata/herdr/claude/manifest.toml`) says what each capture is; tests read it, never file names. | Sanitizing first turns the keymap's `╌` into `-`, so nothing matches; anchoring to the box stops an old "Do you want to proceed?" in the scrollback from making a question a permission card. Keymaps stay data (parent 23). |
| D2-8 | **The emulator harness is the pager's own Go package**, `bridge/emutest` (ADR 0007). It drives the `lcd349` CLI (`run --detach`, `list --json`, `press`, `wait --log`, `shot`, `lint`, `state set`), runs `pager-bridge serve` on 127.0.0.1 and `fakeherdr`, provisions over the instance's virtual serial port, and compares screenshots with goldens using its own comparator, with masks for the clock and ages. Its tests skip unless `PAGER_EMUTEST=1`. | The kit's scenario steps can't start processes, write to the serial port or seed storage, and its golden package can't be imported from another module; new kit steps would be kit work in no phase |
| D2-9 | **The app core takes raw button edges with timestamps**, not the kit's gestures, and owns all gesture logic. It returns its next deadline, so timings are exact. | Its hold-timing tests aren't tautological, and its host tests don't pull in LVGL. The pager's rules (the 600 ms window, the late chord, the consumed waking press) aren't expressible in the kit's classifier anyway. |
| D2-10 | **Button hints sit in one top band**: status on the left, hint cells on the right under the buttons in their physical order (PWR, then BOOT rightmost). The body grows to 144 px; the approve fill bar fills inside the BOOT cell; the danger banner is the body's first row. Cell positions come from the kit's generated `LCD349_BUTTON_PWR_X_PX` / `LCD349_BUTTON_BOOT_X_PX` (phase 1's task 1.4); until the re-pin, `#ifndef` fallbacks from device.json (PWR ≈ 529, BOOT ≈ 607 px at 640/84) stand in, and lane `board` removes them. Text on screen is ASCII only. | The labels sit next to the buttons they name; two bands follow the kit's design guide; a danger card (128 px) fits; so does the tallest, an MCP call's danger card of 3 arguments, exactly: the banner, its first line, the title and 3 more fill the 144 px (34 + 30 + 4 x 20; golden `card_mcp_danger`; amended 2026-11-02, O-90's second review). The cells are ~77 px (BOOT 72 px), so a hold label takes two lines of the small font, each at most 72 px: `HOLD:` over `APPROVE`, `HOLD 2S:` over `APPROVE`, `NEXT` over `HOLD: EXIT` (amended 2026-10-23, when lane goldens found `HOLD: APPROVE` 105 px wide in Montserrat 12). |
| D2-11 | **herdr's socket shapes come from herdr 0.9.3's own API schema** (in the installed binary; identical to the source at tag v0.9.3, protocol 22), copied into `bridge/testdata/herdr/socket/` and marked "from herdr's schema, not captured". The bridge uses the corrected mapping: one request per connection, one JSON line each way, `params` always sent; `agent.get`/`agent.focus` → `{type:"agent_info", agent}`, `agent.read` → `{type:"pane_read", read:{text,…}}`; the title is `terminal_title_stripped`; `name` is optional (the fallback is `<kind> <pane id>`); agents are keyed by `terminal_id` and addressed by their current `pane_id`; `state_change_seq` is one server-wide counter (per-agent equality still works); `completion_seq` isn't a counter, so the bridge records "done since" itself; a send_keys success means the keys were queued. Errors match on `code`, never on message text. | Phase 0 captured `agent.list` and `agent.get` only; guessing the rest in a lane would bake guesses into the fake herdr. The checkpoint confirms them live. |
| D2-12 | **serve ↔ CLI share files, not a socket**: `serve` writes `status.json` (0600, atomic rename, every 1 s and on change) in the config directory; `status` and `devices list` read it, and a file missing or older than 5 s means "not running". Revoke reaches `serve` because `serve` stats `devices.toml` every 500 ms. A `devices.toml` that won't load pairs no device until it loads again, and `status.json`'s `devices_error` says why (the owner's decision, 2026-10-27, open-items O-51). | No new control surface; the 2 s revoke budget holds |
| D2-13 | **The provisioning contract is in the foundation**: a valid and an invalid example of every `@P` line both ways (JSON shape, `state` and `err` values, the 512-byte line limit, timeouts). `provision` opens the port, then waits up to 120 s for the device's `@P hello` (sent on entering pairing mode: first boot, or the menu's Pair again), so the reset that opening the port causes never cancels pairing mode. Device keys are ed25519 only. | The cli lane's fake peer and the firmware's pairing code test against the same lines; Pair again survives the port's reset |
| D2-14 | **The bridge the board dials is one fixed binary, allowed through the firewall once** (added 2026-10-23, before lane `board`; phase 0's D0-24 for the spike): `~/.run-phase/bin/pager-bridge`, built from `phase-2` at `6bcc7a1` after every bridge lane merged (sha256 `4580e5fe…871f`), and rebuilt at the desk checkpoint for its fixes (2026-10-24: the pane ids, sha256 `3bcf59f5…9f1b`; then titles and folders, sha256 `cdd22dd2…245b`; then herdr's tab names, sha256 `05e08cb2…ba43`; then damaged provisioning lines skipped, sha256 `facd39b2…5489`; and on the acceptance day for its fixes, 2026-10-25, built from `93fb7e8`: sha256 `2a83d2bf…38f0`; then, after the day, for `golang.org/x/crypto` 0.56.0, built from `d18971a`: sha256 `cffd47a1…1196`; then, after the gate, for the whole-phase review's bridge fixes, 2026-10-27, built from `3e06be1`: sha256 `2f3ba5ad…549a`; then for `open-items.md` §B's bridge fixes, 2026-10-27, built from `c33fd05`: sha256 `89db1b03…df77`; then for §C's bridge fixes, 2026-10-27, built from `306b51d`: sha256 `845a870ee3ace13f54b7a1526cc3991c44cc92d9dbce68782fcc96c69fa3f586`), each allowed again, and allowed by the owner with `socketfilterfw`. Lane `board`'s board checks run it (its path from `PAGER_BRIDGE_BIN`, which the lane's harness reads), never `go build` or `go run` a bridge the board dials, and never rebuild it. If the bridge must change, the lane stops and says so. | The Mac's firewall is on, in stealth mode: a freshly built bridge's listener is blocked unattended, and the board can't reach it. A fixed, allowed binary keeps the board's checks unattended. | Task 10.5's precondition |
| D2-15 | **The list names each agent by its herdr tab, and shows its folder** (the owner's decisions at the desk checkpoint, 2026-10-24; worded in the glossary's terms 2026-10-27, the owner's decision, open-items O-54): the name is `<workspace number>.<tab number> <tab label>` from `tab.list` and `workspace.list` (read-only; parent 13.2 amended), else herdr's name, the terminal title or `<kind> <pane id>`, cut at its end at a space or hyphen; the second column is the folder (`at`, a new optional snapshot key) in place of the kind. The screen is near-black on a deeper orange (`#CC5A2A`) by default, with a backlit-green option in the global menu. Rejected: the tab id (`w6:t7`, herdr's internal counter, not what its tab bar shows); the title first (long, and doesn't match a tab); head-and-tail cuts for names (hard to read). | With 11 agents named `claude w6:pE`, the owner couldn't tell which one a page was from, so the acceptance day couldn't be a fair test. | Parent 13.2, parent design §Bridge components (`watch`) |

## Seams under test

**Status: confirmed 2026-10-22** (grilling items 11–19). No test is written at a seam that isn't confirmed.

| Seam | Interface | Catches | Misses | Adapters at the boundary |
|---|---|---|---|---|
| **S2-1 Bridge, end to end** | `devsim` (an SSH client speaking NDJSON) → `pager-bridge serve` → the herdr socket | Full-key auth both ways; no password or keyboard-interactive; MaxAuthTries; snapshots and their fields; blocked flips; approve and deny keys; all three stale paths; the per-agent verdict lock; `herdr_error`; question cards refusing verdicts; unknown agent kinds; jump; interrupt; revoke; herdr offline and online; the herdr client's timeout and error envelopes; oversize and invalid input; the rate limit; keepalives answered false; rejected channel and request types; the hello and idle timeouts; session replacement; the 16-agent cap; the method allowlist; no prompt text in logs; the bridge legs of N1 and N2; N4 | Firmware; real herdr's timing | **Fake herdr** (`internal/herdrtest`, a Unix socket answering with the shapes in `testdata/herdr/socket/` and phase 0's captures, recording every method called; `cmd/fakeherdr` runs it as a program). Real loopback SSH. A fake clock where timing is asserted. Every test passes an explicit socket and a temp `HOME`; none can reach the owner's herdr. |
| **S2-2 Card building and danger rules** | `card.Build(info, detection, keymap, rules) → Card`; `risk.Match(command or path)` | The prompt box; command or path first; permission vs question; the summary line; the hash stable across captures and sensitive to one character and to the risk flag; danger label and fragment surviving truncation; git's global options; sensitive paths; limits; each default rule's own cases | Transport, sessions | None (pure). **Expected values come from phase 0's fixtures through the manifest** (D2-7). |
| **S2-3 Sanitizer** (security filter) | `sanitize.Text(in, limit) → out` | ANSI CSI, OSC and DCS; bidi and zero-width code points; C0 and C1; transliteration; non-ASCII becomes `?`; whitespace folding; head-and-tail truncation. Fuzz property: the output is always printable ASCII within its limit. | Card semantics (it runs last, D2-7) | None (pure) |
| **S2-4 Keymap discovery** | `keymap.Load` plus the discovery check over each embedded keymap and its manifest | A keymap that no longer matches its agent's prompts (for example a new Claude Code version); invalid overrides; unknown kinds | Live behaviour | Phase 0's fixtures and the manifest |
| **S2-5 Bridge CLI as a process** | `pager-bridge init`, `status`, `devices`, `provision` with a temp `HOME` | Byte-identical re-init and file modes; the missing-socket warning; `status.json` read and staleness (D2-12); list and revoke; provisioning succeeding, rejected, timing out, given a non-ed25519 or invalid key, or waiting for a late `@P hello` (Pair again); 4 networks with passwords on stdin and never printed; re-provisioning without duplicates; `devices.toml` untouched on failure; the `--host` default; nothing reads stdin but provision's passwords | The real serial port | **Fake serial peer** (a pty) speaking the provisioning fixtures (D2-13) |
| **S2-6 Firmware app core** (D2-1, D2-9) | `app_handle(&state, event, now) → actions` on the host (Unity). **Events:** raw lines from the bridge (the codec is inside), link states (connecting, connected, auth refused, host key changed, down), Wi-Fi state, power source, raw button edges with timestamps, touch (ignored), `@P` provisioning lines, the deadline, boot (stored settings). **Actions:** the render model, tone, backlight, send line, storage write and erase, RTC write, reboot, link control, serial reply, and the next deadline. | Sort order (seen done with idle); the status bar and rows; selection; the ON BATTERY flag; unknown never shown as idle; the 3 s debounce exactly; one page per agent and seq across reconnects; 5-minute reminders; auto-clear; silent toggle at 2 s; the 60 s backlight limit; the wake-only press consumed whole; the 500 ms card guard; hold-to-approve timings (1 s, 2 s for danger); release windows that send nothing; PWR deny; back; question cards; the pocket fuzz (10,000 seeded runs never approve); touch never acting; interrupt confirm; jump; menu navigation; settings and device info; the chord only when both go down together; Pair again; dropping a verdict while the link is down or drops; the 60 s card timeout; backoff and the 5-minute retry limit; codec overflow at 8,193 bytes and malformed input; the device's own text filter; no paging from stale data; clock sync | Rendering, real buttons, the radio | A fake clock (the `now` argument and the returned deadline). Events are built from the protocol and provisioning fixtures where they carry lines. |
| **S2-7 The pager in the emulator** | `bridge/emutest` (D2-8, `PAGER_EMUTEST=1`) against the real firmware (emulator build, kit by commit), a real `pager-bridge serve` on 127.0.0.1 and `fakeherdr` | Pairing over the virtual serial port; every desk-pager screen (goldens, clock and ages masked); end-to-end approve, deny and stale; danger; question; menus; NO WIFI, link down, HERDR OFFLINE, NOT AUTHORISED, HOST KEY CHANGED; the kit's lint on every screen; no Wi-Fi password in any log | The radio, the board's SSH library (the emulator uses Homebrew libssh), sound, timing on hardware | The kit's emulator through the `lcd349` CLI; the fake herdr |
| **S2-8 Board measurements** | Test-build firmware on the board (re-pinned to `v0.1.0`) with `@T` hooks and timestamped logs; the bridge on the Mac; the fake herdr scripting states | Pairing on hardware, `.local` resolution, N1, N2, N3, N7 (N6 dropped by the owner, 2026-10-24) | Real herdr and Claude Code (covered by the checkpoint) | The fake herdr; the `@T press` injection |
| **S2-9 Guided checkpoint and the acceptance day** | The owner following on-device and `HANDOFF.md` prompts with real herdr and Claude Code | Real prompts, real keys, herdr's live shapes (D2-11), sound, feel, the full loop, one real day | Statistics | The owner |

How the parent plan's unit tests fold into these seams:
- the `watch`, `protocol`, `session` and `sshserver` tests become S2-1 slices;
- the `model`, `alert` and `router` tests become S2-6 slices.

The parent's test lists stay the checklist of behaviours to cover.

## Test strategy

- **Walking skeletons first.**
  - S2-1: a paired `devsim` gets a snapshot.
  - S2-6: a snapshot line produces a sorted render model.
  - S2-7: the firmware boots unpaired in the emulator and shows "PAIR VIA USB".
  Each later slice adds one behaviour through the whole stack it needs.
- **Fakes only at boundaries:** the herdr socket, the clock, the serial port and the board's buttons (test builds only). Real SSH, real files, real processes.
- **Expected values** come from phase 0's fixtures (through the manifest), the protocol and provisioning fixtures, herdr's schema shapes, the parent spec's numbers (3 s, 5 min, 1 s, 2 s, 512 bytes, 8,192 bytes) and worked examples written by hand in the test.
- **Nothing reaches the owner's herdr or config:** the runner gives sessions no `HERDR_*` variables; every bridge test uses a temp `HOME` and an explicit fake socket in a short temp path (`os.MkdirTemp("", "hp")`, for macOS's 104-byte socket limit); servers bind `127.0.0.1:0`.

## Unattended execution

| Work | Marker | Notes |
|---|---|---|
| The foundation (groups 0–1) | auto | The bridge's contract and every dependency |
| The bridge lanes (P-10 to P-14, N4) | auto | Host only |
| Firmware lanes `fw`, `core`, `pairing`, `goldens` | auto | Host and emulator, against the kit by commit |
| Lane `board` (the re-pin, the board build, ON BATTERY, P-17 and P-19 on the board) | auto, board | `needs:` the kit's `v0.1.0`; held back while no board is present; holds the board lock for its sessions and the 24-hour wait (dropped with N6 by the owner, 2026-10-24). The bridge runs on the Mac; Wi-Fi comes from `test-wifi.toml` through a host tool. |
| Smoke test, guided checkpoint, acceptance day | human | Batched in the merge stage; real herdr and Claude Code |

- **Spend:** capped for the phase, and split between the lanes and a shared pool (the runner's block).
- **Allowlist:** `go` build, test, vet, run, mod and get; the kit's boardtest by `go -C ~/lcd349-kit run`; `idf.py`; `lcd349` (the kit's CLI); `pkg-config --modversion`; `chmod +x`; the firmware's host-test script; read-only `git`; valvur's `check_package`. Not `herdr` (it reaches the owner's live panes), and not `git add` or `git commit` (the runner commits).

### Lanes

**Foundation (groups 0–1): the contract (D2-6).** It needs only phase 0 and this grilling's fixtures:
- the protocol fixtures and the provisioning fixtures;
- herdr's socket fixtures (copied in by the grilling) decoded by a test, and the Claude fixture manifest;
- the bridge module with every dependency imported (`golang.org/x/crypto` v0.42.0, `golang.org/x/sys` v0.36.0, `github.com/pelletier/go-toml/v2` v2.2.4, `github.com/creack/pty` v1.1.24; no cobra);
- every package's skeleton:
  - each CLI subcommand in its own file under `cmd/pager-bridge/`, with `main.go`'s subcommand switch on stdlib `flag`;
  - `internal/session` split into `session.go` and `verbs.go`;
  - `bridge/keymaps/embed.go` (package keymaps, `//go:embed *.toml`);
- the `devices.toml`, `config.toml` and `status.json` code.

| Lane | Groups | Depends on | Owns |
|---|---|---|---|
| `cli` | 2 | foundation | `init`, `devices` and `provision` commands; `internal/config`, `internal/devices`, `internal/provision` |
| `cards` | 3 | foundation | `internal/sanitize`, `keymap`, `risk`, `card` |
| `e2e` | 4 | foundation | `serve`, `stdio` and `status` commands; `cmd/fakeherdr`; `internal/herdr`, `herdrtest`, `watch`, `protocol`, `sshserver`, `devsim`, `repocheck`; `session/session*.go`; `bridge/e2e/`; `docs/measurements.md` |
| `fw` | 5 | foundation | `firmware/` (it lays out the files the firmware lanes fill, and the platform glue); the harness core `bridge/emutest/harness*.go` |
| `core` | 6 | `fw` | `firmware/main/app/`, `firmware/test_host/` |
| `verdicts` | 7 | `cards`, `e2e` | `session/verb*.go`, `bridge/e2e/verb*_test.go` |
| `pairing` | 8 | `cli`, `e2e`, `core` | `firmware/main/net/`, `prov/`, `app/pairing*`, `ui/screen_pairing*`, `ui/screen_link*`, `scenarios/pairing/`, the isolation test; `bridge/emutest/pairing_*` (the harness's bridge and fake-herdr helpers) |
| `goldens` | 9 | `core`, `verdicts`, `pairing` | `firmware/main/ui/screen_{list,card,menu,status,confirm,toast,info}*`, `firmware/scenarios/desk/`; `bridge/emutest/desk_*` |
| `board` | 10 | `pairing`, `goldens`; **needs** the kit's `v0.1.0`, and the board | `firmware/` (the re-pin, the board build, the power-source glue, `testhooks/`, `board_checks/`), `tools/measure/`, `docs/measurements.md`, this spec's `n6/` |
| `docs` | 11 | `cli`, `e2e` | `README.md`, `docs/bridge.md`, `contrib/launchd/`, `CLAUDE.md`, `bridge/e2e/launchd*_test.go` |

- **No lane owns** `bridge/go.mod`, `bridge/go.sum` (generated: the merge steps tidy it), `bridge/cmd/pager-bridge/main.go`, `bridge/keymaps/` (phase 0's expected values), the fixtures under `testdata/` or this spec. A lane that needs one of them stops and says why.
- **Waves:**
  - **Wave 1:** `cli`, `cards`, `e2e` and `fw`, three at a time.
  - **Wave 2:** `core` (after `fw`), `verdicts`, `docs`.
  - **Wave 3:** `pairing`, then `goldens`.
  - **Later, with the board and the kit's tag:** `board`, then the merge stage.
  Three sessions run at once at most, across both repos.
- **Overlapping paths:** some lanes' paths overlap one they depend on, directly or through others (`core`, `pairing`, `goldens` and `board` inside `fw`'s `firmware/`; `verdicts`' and `docs`' tests inside `e2e`'s `bridge/e2e/`; `board`'s `docs/measurements.md` after `e2e`'s). Preflight allows this, because such lanes never run at the same time.
- **The critical path:** `fw` → `core` → `pairing` → `goldens`, then (with the board) `board` (the 24-hour run was dropped by the owner on 2026-10-24).
- **The merge stage (groups 12–14):**
  - traceability;
  - the smoke test, the desk checkpoint and the acceptance day;
  - the records;
  - the whole-phase review;
  - the gate;
  - the records for the commit; the runner squashes and tags.
- **Estimate:** 64–90 agent hours (the draft's 52–77, plus the fake herdr program, the emulator harness, the platform glue and about 40 more slices). **Wall clock:** about 14–20 hours of lanes before lane `board`; then lane `board` with the 24-hour run (dropped with N6 by the owner, 2026-10-24), the checkpoint and the acceptance day.

### The runner's block

The `run-phase` block that ran this phase (its model, spend cap, lane caps and the agents' allow and deny rules) is kept with the phase's private run logs.

## Risks

| Risk | Mitigation |
|---|---|
| Phase 1 changes something the firmware relies on before the re-pin | The public API is unchanged by phase 1's spec (checked 2026-10-22); the board lane's first task re-pins, rebuilds both targets and reruns the host tests and the emulator harness before any board work |
| herdr's `agent.read`, `send_keys` and `focus` shapes come from its schema, not a capture | Copied from the schema in the installed binary and checked against the source at v0.9.3; tests match error `code`s only; the checkpoint exercises them live |
| The emulator once lost a log line (seen in the kit's eval pilot) | The harness waits on screen state or retries a log wait once, and never relies on a single line where a screenshot or state read would do |
| macOS limits Unix socket paths to 104 bytes | Fake herdr and emulator sockets live in short temp paths (`os.MkdirTemp("", "hp")`; the runner's short instance directory) |
| The emulator's libssh (Homebrew 0.12.2, OpenSSL) isn't the board's (`david-cermak/libssh` 0.12.2, mbedTLS) | `net/ssh_link` hides the library; x/crypto's default algorithms stay unrestricted; lane `board` proves pairing and the link on hardware |
| Claude Code's prompt text changes before the acceptance day | S2-4 fails loudly; re-capture with phase 0's recipe (repeatable) and update the manifest |
| Board scheduling clashes with phase 1 | The board lock; lane `board` needs the kit's tag, which phase 1 makes only after its own board work |
| A lane needs something the foundation didn't lay down (a message type, a signature, a file) | The lane stops with its reason; the merge stage or the owner adds it to the contract, and the lane resumes |
| The bridge and the firmware drift from the protocol or provisioning fixtures | Both test against `testdata/`; the whole-phase review checks they read them the same way |
| ~~The 24 h N6 run ties up the board~~ | Gone: the owner dropped N6 on 2026-10-24 |
| `.local` names don't resolve on the board | lwip's one-shot mDNS query is enabled; the fallback is provisioning with the Mac's IP |
