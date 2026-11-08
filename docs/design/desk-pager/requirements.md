# Requirements: Phase 2, the desk pager

**Status:** grilled 2026-10-22 (grilling queue items 11–19; the owner took every recommended answer). Every open decision below is settled.

## Introduction

The desk pager (`GLOSSARY.md`, ADR 0002), the initiative's MVP:
- on home Wi-Fi, on the desk and on USB power;
- it pages within 6 s when a Claude Code agent in herdr is blocked, and shows the agent's own prompt;
- it approves with a 1 s BOOT hold or denies with PWR, refusing anything stale;
- it pairs once over USB and checks full keys in both directions.

The same firmware runs in the kit's emulator, with golden screenshots.

- **Parent spec:** `.kiro/specs/herdr-pager/` (its requirements are cited as "parent N.M").
- **Definition:** `docs/product/mvp-scope.md` §Desk pager, ADR 0002.
- **Built on:**
  - phase 0: the SSH library decided in D4, the fixtures and the keymap;
  - phase 1: the kit's public API, pinned by commit until its local `v0.1.0` exists, then by that tag (R2.1).
- **Without the board** (settled decision 8): everything that needs neither the board nor the owner runs first: the foundation, the bridge lanes and the firmware lanes on the emulator. Lane `board` waits for the kit's `v0.1.0` and the board; the merge stage comes after it.

## Parent acceptance criteria in this phase

Every criterion below holds in this phase exactly as the parent states it, unless a requirement in the next section narrows it.

| Parent requirement | In this phase | Deferred (phase) |
|---|---|---|
| 1 Bridge setup and identity | 1.1–1.5 | — |
| 2 Pairing over USB | 2.1–2.10; 2.4 narrowed to ed25519 keys only (R4.2) | — |
| 3 Connection and authentication | 3.1–3.8 | — |
| 4 Monitoring herdr | 4.1–4.6; 4.1 as a fixed 1 s poll (R5.3) | — |
| 5 Agent list | 5.1–5.4, 5.5 (buttons), 5.6, 5.7 | Touch scrolling (3) |
| 6 Paging | 6.1, 6.2 (pulled in by the owner on the acceptance day, 2026-10-25), 6.3, 6.4, 6.5, 6.7, 6.8; 6.6 for silent mode only | 6.6 quiet hours (3) |
| 7 Cards, acknowledgement, seen | 7.1–7.7; 7.6 without its touch reset (R1.3) | — |
| 8 Approve and deny | 8.1–8.8 | — |
| 9 Menu | 9.1, 9.2, 9.4, 9.7; 9.5 for working agents only (R6.3); 9.6 without quiet hours and power off | 9.3 snooze, the quiet-hours and power-off items (3) |
| 10 Device power | 10.1, 10.3, 10.7, plus the "ON BATTERY" flag (R1.1, R8.4) | 10.2, 10.4, 10.5, 10.6 (3) |
| 11 Bridge operation | 11.1–11.6 | — |
| 12 Device management | 12.1–12.3 | — |
| 13 Security constraints | 13.1–13.9; 13.4's sanitizing comes last, after matching (R6.1) | — |
| 14 Vibration motor | — | (3, optional) |
| 15 Touch navigation | 15.1, 15.3 (invariants only) | the rest (3) |
| 16 Networks | 16.1–16.4 | 16.5, 16.6 (3) |
| 17 Tailnet | — | Dropped (ADR 0005) |
| 18 Danger-aware cards | 18.1–18.7 (18.6 in the README) | — |
| 19 Runs in the kit's emulator | 19.1, 19.2, 19.4, 19.5 (run locally, through the pager's own harness: R7) | 19.3 in CI (5) |
| 20 Typed approve upstream | — | parallel track (5, optional) |
| 21 Published metrics | — | 21.1–21.3 the owner's statistics (3); 21.4 README numbers (5) |
| 22 One-click install | — | (5) |
| 23 Keymaps as tested data | 23.1–23.3 | 23.4 contributor guide (5) |

Non-functional, in this phase: N1, N2, N3, N4, N7, N8. Dropped by the owner (2026-10-24): N6, the 24-hour run. Deferred: N5, N9, N11 (3); N12 (5). N10 is dropped (ADR 0005).

## Requirements

### Requirement 1: Desk operation

**User Story:** As the owner, I want the desk pager to assume the desk, so that it ships without the portable-power work.

#### Acceptance Criteria

1. THE desk pager SHALL run on USB power. WHEN it is running on its battery instead (the cable is out) THEN the status bar SHALL show "ON BATTERY", so that it never fails silently (the power source comes from the kit: R8.4). It SHALL NOT show a battery level, page on a low battery, or offer power off; those arrive with the portable pager (phase 3).
2. WHILE no button is pressed for 30 seconds and no page is active, THE device SHALL turn the backlight off (parent 10.3). WHEN a button press turns it on, that press SHALL only wake the screen (parent 10.7).
3. Touch SHALL NOT produce any action in this phase, nor reset the card timeout (parent 7.6's touch clause). Touch never wakes the screen (parent 15.1), and buttons drive everything (parent 15.3).

### Requirement 2: Building on phases 0 and 1

**User Story:** As the developer, I want the firmware to build on settled foundations, so that nothing here re-decides phase 0 or phase 1.

#### Acceptance Criteria

1. THE firmware SHALL reach the board only through lcd349-kit (parent 19.1), as a local git dependency (`git+file`, because nothing is published). Until the kit's local `v0.1.0` exists, it SHALL pin the kit by commit (`7c125c5`, the kit's `main` when phase 2 was grilled) and build for the emulator only; lane `board`'s first task SHALL re-pin it to the tag `v0.1.0`. It SHALL NOT use a path override, because the kit's checkout moves to `phase-1` while phase 1 runs.
2. `net/ssh_link` SHALL use the SSH library and key type that phase 0 settled in design D4: `david-cermak/libssh` 0.12.2 for the `esp32s3` target only, and the Mac's libssh 0.12.2 (Homebrew, through pkg-config) on the `linux` target. It is the only file that includes either library's headers, and the only target-specific code outside the kit (parent 19.2).
3. THE bridge's tests SHALL use phase 0's fixtures (`bridge/testdata/herdr/claude/`) and `keymaps/claude.toml` as their expected values, classified by a manifest (`manifest.toml`: each fixture's kind, prompt type, whether it was captured, and its expected command, path or label), never by file name. A prompt phase 0 recorded as "not captured" (its `DETECTION.md`) is treated as unknown: its card offers only jump. Where phase 0 has a single capture of a prompt, the hash-stability check is skipped for it, and one-character sensitivity is shown with variants written by hand.

### Requirement 3: The acceptance day

**User Story:** As the owner, I want to use the pager for a real day before calling it done, so that the desk pager is proven by use, not only by tests.

#### Acceptance Criteria

1. THE desk pager SHALL be accepted only after one full working day in which every Claude Code permission prompt in herdr is answered from the pager.
2. THE day's bridge log SHALL show no `approve` that the owner didn't give (checked against the owner's notes), and no stale prompt approved.
3. Any defect found that day SHALL become a failing test at an agreed seam before it is fixed.
4. AFTER acceptance, days in use (`GLOSSARY.md`) SHALL be recorded in `docs/product/backlog.md`. Seven of them open phase 3 (ADR 0001).

### Requirement 4: Provisioning, the bridge's side

**User Story:** As the owner, I want pairing to work the same from both ends and to survive the board's reset, so that pairing a pager is one command and one button press.

#### Acceptance Criteria

1. THE provisioning lines (`@P`, parent design §Provisioning) SHALL be a contract fixed before either side is built: `bridge/testdata/provisioning/` holds a valid and an invalid example of every line in both directions, with their states, errors, the 512-byte line limit and the timeouts. THE bridge's tests and the firmware's SHALL both read it.
2. THE tool SHALL accept only an ed25519 device key (design D4), and SHALL refuse any other type with a precise error, leaving `devices.toml` unchanged (narrows parent 2.4).
3. IF `--host` is omitted THEN the tool SHALL use `<LocalHostName>.local`, read with `scutil --get LocalHostName` (a fixed argument vector, which parent 13.3 allows), and SHALL fall back to the host name if that fails (parent 2.9).
4. WHEN `provision` opens the serial port THEN it SHALL wait up to 120 seconds for the device's `prov_hello`, which the device sends on entering pairing mode and every 2 seconds while in it, and SHALL tell the owner to choose **Pair again** on the device now. Opening the port resets the board, so pairing mode SHALL be entered after the port is open, never before (parent 2.10).
5. THE tool SHALL NOT print or log a Wi-Fi password, and SHALL read passwords only from stdin (parent 2.3, 13.8).

### Requirement 5: The bridge's herdr client and its files

**User Story:** As the developer, I want the bridge to speak the herdr that is installed, and the CLI to see the running bridge, so that nothing rests on a guessed shape.

#### Acceptance Criteria

1. THE herdr client SHALL speak herdr 0.9.3's socket API (protocol 22) as its own schema states it (`bridge/testdata/herdr/socket/`; parent design §Bridge components): one request per connection, one JSON line ending in `\n` each way, `params` always sent, a 2-second deadline, and each result checked for the `type` its method expects. herdr's errors SHALL be matched by `code`, never by message text.
2. THE bridge SHALL key agents by herdr's `terminal_id` and address them by their current `pane_id`. A display name SHALL be herdr's `name` when there is one, else `<kind> <pane id>`.
3. THE bridge SHALL poll `agent.list` once per second; the interval is not configurable (parent 4.1).
4. THE bridge SHALL NOT check herdr's protocol version, because `ping` is not one of the five allowed methods (parent 13.2). An `agent.list` reply that doesn't decode SHALL be treated as a herdr error, shown to devices and logged with herdr's error code.
5. WHILE `serve` runs THEN it SHALL keep `status.json` (mode 0600, replaced atomically, every second and on change) in its configuration directory, and `status` and `devices list` SHALL read it. A missing file, or one older than 5 seconds, SHALL mean "not running".
6. `serve` SHALL notice a changed `devices.toml` within 500 ms, so that `devices revoke` closes the device's session within 2 seconds (parent 12.2).
7. No test SHALL reach the owner's live herdr: every test SHALL use an explicit fake socket and a temporary `HOME`, and the runner gives sessions and checks no `HERDR_*` variables.

### Requirement 6: Cards

**User Story:** As the owner, I want a card to show exactly the prompt I'd see at the keyboard, judged on its real text, so that sanitizing can never hide or invent a match.

#### Acceptance Criteria

1. THE bridge SHALL match prompts on their raw detection text, and SHALL sanitize only for display, last (ADR 0006): the prompt box (below the keymap's `box_start`) → `permission_match` and the question kinds on the raw box → `command_match` or `file_match` → the danger rules on the raw command or path → sanitizing each display field. `permission_match` SHALL see only the prompt box, never the scrollback above it.
2. A card's summary SHALL be the prompt box's title line, and the keymap's `drop_lines` SHALL keep tips and rules out of it. Both are data in the keymap, not code (parent 23).
3. Interrupt SHALL be offered and accepted only for a working agent (narrows parent 9.5: on a blocked prompt, the interrupt key is a deny that would skip the stale check). A question card SHALL offer only jump.
4. Danger rules for git SHALL allow git's global options (`-C dir`, `-c k=v`, `--git-dir=…`, `--work-tree=…`) between `git` and the subcommand, and phase 0's danger fixture SHALL be one of their cases.
5. AN edit or create prompt's card SHALL put the file path first, and the sensitive-path rules (parent 18.7) SHALL apply to that path.

### Requirement 7: The pager in the emulator

**User Story:** As the developer, I want to drive the whole pager, bridge and fake herdr together in the emulator, so that every screen and flow is checked without the board.

#### Acceptance Criteria

1. THE emulator tests (seam S2-7) SHALL run through the pager's own Go harness, `bridge/emutest` (ADR 0007). It SHALL drive the kit's `lcd349` CLI, run a real `pager-bridge serve` on 127.0.0.1 and the fake herdr, provision over the emulator instance's virtual serial port, and compare screenshots with goldens using masks for the clock and ages.
2. THE harness's tests SHALL be skipped unless `PAGER_EMUTEST=1` is set, so the bridge's ordinary suite doesn't need the firmware built.
3. A runnable fake herdr (`bridge/cmd/fakeherdr`) SHALL serve a socket from a scenario file of agent states over time, or churn them at a fixed interval, and SHALL log every request it receives. The harness, N4 and N6 use it.
4. Every desk-pager screen SHALL have a golden and pass the kit's lint, including the permission and danger cards, the confirms, the toasts, PAIR VIA USB, NOT AUTHORISED, HOST KEY CHANGED and NO WIFI (parent 19.4, 19.5).

### Requirement 8: The screen

**User Story:** As the owner, I want each button's meaning shown beside that button, so that I never have to remember which is which.

#### Acceptance Criteria

1. THE screen SHALL have one top band and a body. The band SHALL show the status on the left and a hint cell under each button on the right, in their physical order: PWR, then BOOT. The body SHALL be the remaining 144 px. While BOOT is held on a permission card, the BOOT cell SHALL fill as the hold's progress bar, and a danger card's banner SHALL be the body's first row.
2. THE hint cells' positions SHALL come from the kit's generated button positions (phase 1's task 1.4). Until the kit is re-pinned to `v0.1.0`, the firmware MAY define them from `device.json`'s values, and lane `board` SHALL remove those fallbacks.
3. Every string on screen SHALL be printable ASCII, because the fonts carry only ASCII: "OK" and "X" for a verdict's result, "-" for a dash. Confirm screens SHALL put each button's meaning in its own hint cell, so no string fixes the buttons' order.
4. THE "ON BATTERY" flag (R1.1) SHALL come from the kit's power source (`lcd349_power_source()` and its event, which phase 1 adds). IF the kit reports the source as unknown THEN no flag SHALL be shown.

## Non-functional requirements in scope

These are the parent's budgets, measured as the parent states, except where the "How" column differs.

| ID | Budget | How (this phase) |
|---|---|---|
| N1 | Page latency ≤ 6 s p95 | Board, 20 trials; the fake herdr flips a scripted agent to blocked, and both ends are timestamped on the Mac: the fake herdr's flip, and the device's page line as it arrives on the serial port (the `now` clock sync is too coarse to compare the device's own clock) |
| N2 | Verdict latency ≤ 1.5 s p95 | Board, 20 trials, through a test-build button injection (`@T press`, which is never in release builds), from the injected hold's completion (its write on the Mac plus the hold) to the fake herdr's `send_keys`; 3 confirming real presses in the checkpoint |
| N3 | Reconnect ≤ 35 s; snapshot resumes ≤ 2 s | Board, 5 bridge restarts and 5 fake-herdr restarts, scripted |
| N4 | Bridge RSS ≤ 40 MB, CPU ≤ 1 % | On the Mac, in lane `e2e` (no board): `ps` sampling for 5 min, 10 agents from the fake herdr |
| N6 | 24 h with no reboot; heap within 10 % | Board, fake herdr churning every 10 s (**dropped by the owner, 2026-10-24**: no 24-hour run in phase 2; its dry run had started) |
| N7 | Boot to connected ≤ 15 s, refused joins included (parent N7, amended 2026-10-27: open-items O-45) | Board, 5 cold boots by harness reset |
| N8 | Every bridge acceptance criterion has an automated test | Traceability table |

## Open decisions (settle in grilling)

All settled in grilling items 11–19 on 2026-10-22; the owner took every recommended answer.

1. **The in and deferred table above.** Settled: accepted, as narrowed by R4–R8. Settled at program level earlier: the danger banner and tone, 1–4 networks, the acceptance day, the "ON BATTERY" flag.
2. **Firmware logic behind one deep seam** (the app core: events in, actions out), not four module seams. Settled: yes (`design.md` §Seams).
3. **The firmware's host test framework.** Settled: Unity, already in ESP-IDF and the kit, instead of vendoring doctest.
4. **N2 measured with a test-only button injection,** plus 3 real presses. Settled: yes; it keeps the measurement unattended.
5. **The kit pin.** Settled: by commit (`7c125c5`, `git+file`) while the kit has no tag, so the firmware lanes run on the emulator now; lane `board` re-pins to `v0.1.0` (R2.1). No path override.
6. **The spend cap.** Settled; the figures are kept with the phase's private run logs.
7. **The `run-phase` block.** Settled; it's kept with the phase's private run logs.
8. **What runs without the board.** Settled: one phase, run as far as it goes now (the foundation, the bridge lanes and the firmware lanes on the emulator); lane `board` waits for the kit's tag and the board, and the merge stage follows it. The alternatives were the firmware waiting for the tag, or splitting the phase.
9. **The `herdr` permission** (queued for item 12). Settled: removed. It covers sending keys, stopping the server and remote hosts, and no task needs the real herdr. No test reaches it (R5.7).
10. **The bridge's dependencies.** Settled: `golang.org/x/crypto` v0.42.0 (the spike's), `golang.org/x/sys` v0.36.0 (termios for the serial port, so no serial library), `github.com/pelletier/go-toml/v2` v2.2.4, and `github.com/creack/pty` v1.1.24 (tests only), all checked by hand because valvur answers "unknown" for Go modules. No cobra: the standard library's `flag`.
11. **herdr's socket shapes.** Settled: taken from herdr 0.9.3's own API schema, not captured live (R5.1); the desk checkpoint confirms them.
12. **Card matching.** Settled: raw text matched, display sanitized last (R6.1, ADR 0006).
13. **Where the button hints go** (queued for item 16). Settled: one top band, a cell under each button (R8.1). Rejected: swapped bands, a bottom bar, a right-hand column, no hints.
14. **The emulator tests.** Settled: the pager's own Go harness drives the kit's CLI (R7, ADR 0007), rather than new kit scenario steps that no phase builds.
15. **The app core's input.** Settled: raw button edges with timestamps, not the kit's gestures, so hold timings are tested on a fake clock and the host tests don't need LVGL.
16. **ON BATTERY.** Settled: a kit API (R8.4), added to phase 1; the firmware's glue and its golden come in lane `board`, after the re-pin.
17. **Kit amendments in phase 1's spec** (lcd349-kit). Settled: `lcd349_power_source()` and its event in the power lane; generated button positions in task 1.4; `boardtest -C <project>` in task 1.3.
