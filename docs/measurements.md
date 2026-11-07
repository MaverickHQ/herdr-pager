# Measurements

Phase 2's non-functional numbers as measured: what, on what, when and how (phase 2 requirements §Non-functional; the budgets are the parent requirements' N1–N12). A FAIL stops the lane that measured it. What the desk checkpoint's and the acceptance day's logs show is in [§The desk checkpoint and the acceptance day](#the-desk-checkpoint-and-the-acceptance-day).

| N | Budget | Result | Measured |
|---|---|---|---|
| N1 | Page latency ≤ 6 s p95 | **PASS**: p95 4.130 s over 20 blocked flips (3.952–4.156 s). At the gate: p95 4.048 s | 2026-10-23, lane `board` (task 10.7), on the board, timed on the owner's Mac; again 2026-10-26, the gate (task 14.1) |
| N2 | Verdict latency ≤ 1.5 s p95 | **PASS**: p95 0.058 s over 20 `@T` holds (0.041–0.061 s). The 3 confirming real presses at the desk checkpoint took 0.044–0.047 s. At the gate: p95 0.073 s | 2026-10-23, lane `board` (task 10.8), on the board, timed on the owner's Mac; the real presses 2026-10-24, the desk checkpoint (task 12.3); again 2026-10-26, the gate (task 14.1) |
| N3 | Reconnect ≤ 35 s; snapshot resumes ≤ 2 s | **PASS**: after 5 bridge restarts the link was back in 0.561–0.873 s, and the list 0.062–0.072 s after it; after 5 fake-herdr restarts the list was back in 0.942–1.044 s. At the gate: 0.616–0.972 s, 0.064–0.070 s and 0.836–1.048 s | 2026-10-24, lane `board` (task 10.9), on the board, timed on the owner's Mac; again 2026-10-26, the gate (task 14.1) |
| N4 | Bridge RSS ≤ 40 MB; average CPU ≤ 1 % of one core, 10 agents polled at 1 Hz | **PASS**, on `pager-bridge` itself: RSS at most 25.8 MiB; `%cpu` 0.77 % on average (at most 2.0 % in one sample); 1.1 s of CPU time in 5 min, 0.37 % of one core | 2026-10-26, group 13's review (task 13.1), on the owner's Mac (macOS, Darwin 25.6.0, arm64; Go 1.26.4). First measured 2026-10-22 by lane `e2e` (task 4.29) on a stand-in build |
| N6 | 24 h connected: no reboot; heap within 10 % | **Not measured**: the owner dropped the 24-hour run on 2026-10-24 | — |
| N7 | Boot to connected ≤ 15 s | **PASS**: 5 boots by harness reset, 6.544–9.885 s (worst 9.885 s). **At the gate, one run went over:** a boot took 16.351 s when the access point refused two joins in a row. Its rerun passed (worst 10.717 s, one refusal). Whether N7 covers refused joins is the owner's call ([§The gate's board run](#the-gates-board-run)). **Again after §B (2026-10-27), timed to the first snapshot:** one boot went over the same way (16.23 s, two refusals); the rerun passed (worst 10.908 s) ([§B's board run](#bs-board-run-open-itemsmd-2026-10-27)) | 2026-10-23, lane `board` (task 10.6), on the board, timed on the owner's Mac; again 2026-10-26, the gate (task 14.1) |

## N4: the bridge's footprint

- **How:** `TestN4BridgeFootprint` (`bridge/e2e/n4_test.go`), run with `PAGER_MEASURE=1`, or `go -C bridge test -count=1 -timeout 15m ./e2e/ -run TestN4BridgeFootprint -v -args -measure`. It skips otherwise.
- **The setup:**
  - `pager-bridge serve` on 127.0.0.1, with a temp HOME.
  - `fakeherdr` serves 10 claude agents, each with phase 0's `shell-0s.txt` as its detection text. They churn every 10 s (`--churn 10s`), so each churn changes every agent and sends a snapshot.
  - One paired device (devsim) stays connected, reads every line, and pings every minute, as a pager does. It got 31 snapshots.
- **Sampling:** `ps -o rss=,%cpu= -p <pid>` every 10 s for 5 minutes, 30 samples, as parent N4 says. The budget is checked against the largest RSS (40 MiB) and the average `%cpu` (1 %). The CPU time (`ps -o time=`) over the same 5 minutes is logged as a cross-check.
- **What was measured (2026-10-26, task 13.1):** `pager-bridge` itself, built by the test from the bridge as it stood in group 13's review. The run took 308 s: `ok herdr-pager/bridge/e2e 308.534s`. Other test suites ran on the Mac during part of it.
- **The samples:** RSS mostly 12.6–14.9 MiB, with two samples higher (23.4 and 25.8 MiB), at most 25.8 MiB. `%cpu` was 0.0–2.0 %, 0.77 % on average. macOS's `%cpu` is a decaying average, so a few samples go over 1 % while the average and the CPU time stay under. 31 snapshots reached the device.
- **The first run (2026-10-22, lane `e2e`, task 4.29):** RSS 11.1–15.7 MiB, `%cpu` 0.48 % on average, 0.84 s of CPU time (0.28 % of one core). Lane `cards`' loaders hadn't merged then, so the process measured was `cmd/pager-bridge`'s test binary running `serve` with stand-ins for the keymap and danger-rule loaders. Group 13 removed that stand-in seam, and the row above replaces that run.

## On the board: N1, N2, N3 and N7

- **When:** one run, 2026-10-23 23:48 to 2026-10-24 00:03 (+01:00), lane `board` (tasks 10.6–10.9): `cd tools/measure && PAGER_MEASURE=1 go test -count=1 -timeout 90m -run 'TestN7BootToConnected|TestN1PageLatency|TestN2VerdictLatency|TestN3Recovery' -v ./...` → `ok … 1012.792s`.
- **The board:** the owner's Waveshare ESP32-S3-Touch-LCD-3.49B, powered by USB-C from the Mac, which is also its serial port. It was on the home 2.4 GHz Wi-Fi with the Mac (RSSI −41 to −74 dBm at the runs' joins).
- **The firmware:** the test build (`firmware/sdkconfig.test`, `CONFIG_PAGER_TEST_HOOKS=y`), with ESP-IDF v5.5.5 and lcd349-kit `v0.1.0` (`ba25ee0`).
  - Each test flashed it again, and every boot in the four serial logs printed the same image (ELF SHA-256 `3a18b0718…`).
  - Its source is as committed in `23a6d90`. Nothing in `firmware/` or `tools/measure/` had changed when the results were recorded (task 10.12). The firmware has changed since, with the desk checkpoint's fixes (2026-10-24), the acceptance day's (2026-10-25) and group 13's (2026-10-26). The gate measured all four again on that firmware ([§The gate's board run](#the-gates-board-run)).
  - It differs from the release build only by the test hooks: `@T` presses in, `@L` log lines out (design D2-4).
- **The Mac:** macOS (Darwin 25.6.0, arm64), Go 1.26.4.
  - The bridge is the fixed binary `~/.run-phase/bin/pager-bridge` (design D2-14). The harness checks its sha256, `4580e5fe…871f`, on every run. It runs `serve` on `0.0.0.0:2277` with a temp HOME (on `0.0.0.0:2278` since 2026-10-27, so the owner's bridge can keep serving: `open-items.md` O-42).
  - herdr is `fakeherdr` (`bridge/cmd/fakeherdr`), with a scenario for each test.
- **The harness:** `tools/measure/` (lane `board`, task 10.4); these four are in `measure_test.go`.
  - Each test pairs the board afresh: the flash is erased, `provision` runs over USB, and `@T press BOOT` confirms. Then the test waits until the bridge sees the board and the list reaches it.
  - p95 is the nearest rank: the 19th of 20.
- **The clocks:** both ends of every number are on the Mac's clock:
  - the harness's own events: fakeherdr's start, `serve: listening`, the reset, and each `@T` line's write;
  - fakeherdr's request log;
  - each serial line, stamped as it reached the Mac.
  - The board's own log clock isn't used; it agrees with the Mac's anyway (N7).
- **Health:** no panic and no unplanned restart in any of the four serial logs. Each has one software restart, the planned one after pairing.
- **The raw results:** `n1.json`, `n2.json`, `n3.json` and `n7.json`, and each test's serial and bridge logs, are in `~/.run-phase/out/<folder>-2/lane-board-2026-10-23/`, outside the repository (`<folder>` is the working copy's folder name). The board checks write to `~/.run-phase/out/<folder>-2/` itself, so each run replaces the last one's files there; the gate copied this run's into that folder first.

## N1: page latency

- **How:** `TestN1PageLatency`. On a connected board, fakeherdr restarts with a script: `api-refactor` is working, turns blocked at 15 s + i × 12 s and is working again 6 s later, 20 times.
- **The two ends:**
  - the flip: when fakeherdr's socket appeared, plus the change's `at`;
  - the page: the board's `@L … page blocked` line (the core's page-tone action), as it reached the Mac.
- **The trials (s):** 4.156, 4.033, 4.015, 3.996, 3.977, 3.956, 4.039, 4.021, 4.003, 3.983, 3.963, 4.047, 4.130, 4.008, 3.989, 3.970, 3.952, 4.034, 4.036, 3.998.
  - p95: 4.130 s.
  - Every flip paged exactly once: there are 20 `page blocked` lines.
- **Why about 4 s:** the core waits out its 3 s debounce before it pages (parent N1: 3 s debounce plus ≤ 3 s pipeline). The bridge polls `agent.list` once a second, which adds up to 1 s.
- **The error:** fakeherdr starts its script's clock just after it makes its socket, and the harness looks for the socket every 20 ms. So each flip may have come up to about 20 ms before the time recorded, and each latency may be up to about 20 ms longer than recorded.

## N2: verdict latency

- **How:** `TestN2VerdictLatency`.
  - fakeherdr's agent shows phase 0's Claude Code shell permission prompt (`bridge/testdata/herdr/claude/detection/shell-0s.txt`, with the title `Bash command`), so its card offers approve from the keymap. It turns blocked every 16 s.
  - Each trial: the page; 1 s later, `@T press PWR 100` opens the card; 1 s after that (past the card's 500 ms guard), `@T press BOOT 1000`, the approve hold.
- **The two ends:**
  - the hold's completion: the `@T press BOOT 1000` line's write on the Mac, plus 1 s;
  - `send_keys`: its `time` in fakeherdr's request log.
- **The trials (s):** 0.048, 0.047, 0.053, 0.046, 0.043, 0.041, 0.044, 0.051, 0.044, 0.049, 0.057, 0.055, 0.052, 0.050, 0.058, 0.061, 0.049, 0.045, 0.051, 0.047.
  - p95: 0.058 s.
  - 20 holds sent 20 `agent.send_keys`, one each.
- **The error:** the board times the hold on its own core clock from when the line arrives (task 10.4 measured 1001 ms). So the count includes the line's trip over USB, and if anything the recorded latency is slightly longer than the real one.
- **The 3 real presses** (the desk checkpoint, task 12.3, 2026-10-24): the owner held BOOT on 3 real cards, on the test build, with real herdr. Each is timed from the test build's `@L … button BOOT down` line plus 1 s, stamped on the Mac by `idf.py monitor --timestamps`, to the bridge log's `verdict sent`: 0.047, 0.044 and 0.044 s. `@T` presses exist only in test builds. The owner's notes are in `~/.run-phase/out/phase-2-desk/checkpoint-notes.md`. Task 12.5 checked the `verdict sent` times against the bridge's log; the press times are the notes' alone (§The desk checkpoint and the acceptance day).

## N3: recovery

- **How:** `TestN3Recovery`: 5 bridge restarts, then 5 fakeherdr restarts, on one connected board.
- **A bridge restart:** `serve` is stopped. Once the board logs `link down` or `connecting`, the harness waits 2 s more, then starts `serve` again.
  - It's timed from `serve: listening` in the bridge's log to the board's `@L … link connected`, then from that to its next `@L … line snap`.
  - The link was back in 0.804, 0.561, 0.632, 0.873 and 0.636 s. The list came 0.072, 0.064, 0.066, 0.062 and 0.072 s after the link.
- **A fakeherdr restart:** fakeherdr is stopped until the HERDR OFFLINE snapshot reaches the board, then 2 s more, then started again.
  - It's timed from its socket appearing to the board's next snapshot line: 1.044, 1.044, 0.946, 0.944 and 0.942 s. That's the bridge's 1 s poll.
- **What it doesn't show:** each restart here was a short outage of a few seconds, so the link's backoff was still at its first steps (1 s, then 2 s).
  - After about half a minute of failed dials, the backoff reaches its 30 s cap. With ±20 % jitter (parent design §Firmware components; `firmware/main/app/link.c`), the board may then wait up to 36 s before its next dial, which is more than N3's 35 s.
  - That comes from arithmetic, not from a measurement, and the spec doesn't say whether N3 covers a long outage. It's left for the merge stage.
  - **Since 2026-10-27** (the owner's decision, `open-items.md` O-48): N3 covers a long outage, and at the cap the jitter only shortens the wait, to 24–30 s. The worst case is then about 31 s: the 30 s wait and the 0.5–1.05 s the link took once the bridge was back.

## N7: boot to connected

- **How:** `TestN7BootToConnected`. A connected board is reset 5 times by the harness, with esptool's hard reset over the USB serial port: RTS high for 200 ms with DTR low. The board logs `rst:0x15 (USB_UART_CHIP_RESET)`.
  - Each boot is timed from letting go of RTS to the board's `@L … link connected` as it reached the Mac.
- **The boots (s):** 6.628, 9.885, 7.166, 6.544, 6.812. The worst is 9.885 s.
- **Counted the parent's way** (parent N7: from power-on to the first snapshot), from the serial log:
  - The chip resets as RTS goes high: its `rst:` line reaches the Mac about 0.2 s before the harness's mark.
  - Counted from the `rst:` line, the link was up after 6.828, 10.084, 7.366, 6.743 and 7.011 s.
  - The first snapshot (`@L … line snap`) came 63–75 ms after the link: 6.894, 10.147, 7.441 and 6.807 s. Boot 5's log ends at its link line. The worst is 10.15 s.
  - The board's own log clock (ms since reset) agrees within 4 ms. For example, boot 2's snapshot line is `I (10148)`.
- **A reset, not a power cycle:** phase 2 measures N7 by harness reset (requirements §Non-functional). The harness can't cut the board's power, so a true power-on wasn't timed.
- **The first attempt failed** (task 10.6): one boot took 69 s.
  - The access point refused the first join, and the kit tried again only at its next scan, 60 s later.
  - The pager now retries a refused join itself, after 1, 2, 4, 8 and 16 s and then every 30 s (`firmware/main/platform/pump.c`).
  - No join was refused in the passing run, so that retry hasn't yet run on the board.

## The gate's board run

- **When:** 2026-10-26, the gate (task 14.1), on the Mac's clock (UTC, the owner's local time):
  - 20:40–21:02, every board check: `cd tools/measure && PAGER_MEASURE=1 go test -count=1 -timeout 90m -v ./...` → `FAIL … 1281.212s`. N7 failed; every other check passed.
  - 21:03–21:06, N7 alone: `… -run '^TestN7BootToConnected$' …` → `ok … 140.384s`.
- **What ran:** as lane `board`'s run (§On the board), on the same board and home Wi-Fi, with three differences:
  - the firmware as group 13 left it (`bde0004`), the test build, ELF SHA-256 `1436977db…` in every serial log;
  - the fixed bridge rebuilt for x/crypto 0.56.0 (D2-14, sha256 `cffd47a1…1196`), which the harness checked;
  - every check, not only the four measurements.

| Check | Result |
|---|---|
| `TestBoardTestHooks` | **PASS**: the release build ignores `@T`; the test build's injected 1 s hold was 1001 ms by the core's clock |
| `TestBoardPairsAndConnects` | **PASS** |
| N1 | **PASS**: p95 4.048 s over 20 flips (3.951–4.057 s) |
| N2 | **PASS**: p95 0.073 s over 20 holds (0.019–0.105 s) |
| N3 | **PASS**: after 5 bridge restarts the link was back in 0.616–0.972 s, and the list 0.064–0.070 s after it; after 5 fake-herdr restarts the list was back in 0.836–1.048 s |
| N7, first run | **FAIL**: 6.973, 6.892, 10.726, 16.351 and 7.650 s |
| N7, rerun | **PASS**: 10.717, 6.889, 6.549, 6.689 and 6.665 s |

- **Why a boot took 16.351 s:** Wi-Fi, not the link. The access point refused the pager's join twice, and the third join went through.
  - The refusals: `auth -> init` 2 ms after the first attempt, and after 1 s on the second. The kit logged `not connected (reason 1)` each time.
  - The pager's join retry (`firmware/main/platform/pump.c`, added after task 10.6's 69 s boot) tried again 1 s, then 2 s, after each refusal. This is the first time it ran on the board.
  - Each attempt starts with the kit's full scan, about 2.5 s, before it connects (`attempt` in the kit's `src/hw/wifi.c`).
  - So the two refusals cost about 11 s: about 3.7 s in the retry's waits, 5 s in two more scans, and the rest in the attempts themselves. The board had Wi-Fi at 15.98 s by its own clock, and the link took 0.57 s more.
- **How often:** 3 of the gate's 10 boots met a refusal (the first run's boots 3 and 4, and the rerun's boot 1), and one of them met two.
  - Every boot without a refusal connected in 6.5–7.7 s. Each boot with one refusal took 10.7 s.
  - Three access points answer for the home network (three BSSIDs in the logs). The serial log doesn't name the one that refused.
  - Lane `board`'s run (2026-10-23) met no refusal.
- **What it means for N7:** the parent's N7 is ≤ 15 s "on known Wi-Fi", over 5 trials, and doesn't say whether a join the access point refuses counts. On this network a boot that meets two refusals goes over, and one that meets one doesn't. The owner decides whether N7 covers that. If it does, the time goes on the retry's waits and the kit's scan before each attempt.
- **Health:** no panic, abort or backtrace in any of the run's serial logs. Their resets are N7's 10 harness resets and 6 software restarts, one planned after each test's pairing.
- **The raw results:** in `~/.run-phase/out/<folder>-2/`, outside the repository: `gate-14.1-run1/` (the full run) and `gate-14.1-n7-rerun/`.

## §B's board run (`open-items.md`, 2026-10-27)

- **When:** 2026-10-27, after `open-items.md` §B, on the Mac's clock (UTC):
  - 11:19–11:41, every board check: `cd tools/measure && PAGER_MEASURE=1 go test -count=1 -timeout 90m -v ./...` → `FAIL … 1131.839s`. N7 and N2 failed; every other check passed.
  - Then N2 and N7 again, after N2's test was fixed (below): `… -run '^(TestN2VerdictLatency|TestN7BootToConnected)$' …` → both PASS.
- **What ran:** as at the gate, on the same board and home Wi-Fi, with these differences:
  - the firmware after §B (`open-items-b` at `6dc9138`), the test build, ELF SHA-256 `3bbd2340c…` in every serial log; it reports `0.1.0-dev+test` (O-42);
  - the fixed bridge rebuilt for §B (D2-14, sha256 `89db1b03…df77`), which the harness checked;
  - the checks' bridge on port 2278, so the owner's bridge served on 2277 throughout (O-42);
  - N7 timed to the first snapshot, the parent's way, with the link's time beside it (O-12);
  - a new check without the board, `TestBuildsTellTheirVariant` (O-42, O-43).

| Check | Result |
|---|---|
| `TestBuildsTellTheirVariant` | **PASS**: the release reports `0.1.0-dev` and holds none of the hooks' text; the test build reports `0.1.0-dev+test` |
| `TestBoardTestHooks` | **PASS**: each variant's boot line has its version; the injected 1 s hold was 1000 ms by the core's clock |
| `TestBoardPairsAndConnects` | **PASS**: connected 7.4 s after provision ended |
| N1 | **PASS**: p95 3.758 s over 20 flips |
| N2, first run | **FAIL**: trials 1–13 sent their keys 0.039–0.158 s after the hold; trial 14 sent none |
| N2, rerun | **PASS**: p95 0.100 s over 20 holds (0.046–0.110 s) |
| N3 | **PASS**: after 5 bridge restarts the link was back in 0.531–1.054 s, and the list 0.067–0.078 s after it; after 5 fake-herdr restarts the list was back in 0.941–1.046 s |
| N7, first run | **FAIL**: the list 8.57, 10.90, 7.34, 16.23 and 7.13 s after the reset (the link 0.06–0.08 s before each) |
| N7, rerun | **PASS**: the list 10.33, 7.38, 10.91, 8.16 and 10.33 s after the reset (worst 10.908 s) |

- **Why N2's trial 14 sent nothing:** the test, not the pager. The card's text came 708 ms after PWR, in a Wi-Fi hiccup (`bcn_timeout` in the serial log), where it usually takes about 55 ms. The test held BOOT a fixed 1 s after PWR, so the hold began 197 ms after the card appeared, inside the card's 500 ms guard (`router.c`, parent 8.1), and the pager ignored it, as it should. The test now waits for the card's `@L … line card` line and 700 ms more before the hold. O-22's 30 ms wait before a hold fires is in every N2 number above.
- **Why N7's boot 4 took 16.23 s:** the access point refused the pager's join twice (`auth -> init`, `not connected (reason 1)`), and the third went through at 13.7 s by the board's clock, as at the gate. Whether N7 covers refused joins is still the owner's call (`open-items.md` O-45).

## §C's board run (`open-items.md`, 2026-10-27)

- **When:** 2026-10-27, 20:09–20:29 on the Mac's clock (UTC), after `open-items.md` §C and O-77: every board check, `cd tools/measure && PAGER_MEASURE=1 go test -count=1 -timeout 90m -v ./...` → `ok … 1196.148s`, first time.
- **What ran:** as at §B's run, with these differences:
  - the firmware after §C and O-77 (`open-items-c` at `06436bc`), the test build, ELF SHA-256 `e6a01ae14…` in every serial log;
  - the fixed bridge rebuilt for §C (D2-14, sha256 `845a870e…f586`), which the harness checked;
  - a refused join tried again at once (O-45), and the backoff's cap never exceeded (O-48);
  - the boot line names why the pager started (O-77): `after a reset` after each of the harness's resets over USB, `after a restart` after each pairing.

| Check | Result |
|---|---|
| `TestBuildsTellTheirVariant` | **PASS** |
| `TestBoardTestHooks` | **PASS**: the injected 1 s hold was 1000 ms by the core's clock |
| `TestBoardPairsAndConnects` | **PASS**: connected 7.9 s after provision ended |
| N1 | **PASS**: p95 3.298 s over 20 flips (3.145–3.331 s) |
| N2 | **PASS**: p95 0.129 s over 20 holds (0.048–0.149 s) |
| N3 | **PASS**: after 5 bridge restarts the link was back in 0.578–1.186 s, and the list 0.063–0.085 s after it; after 5 fake-herdr restarts the list was back in 0.938–0.945 s |
| N7 | **PASS**: the list 7.21, 7.22, 9.63, 6.81 and 6.85 s after the reset (the link 0.06–0.08 s before each; worst 9.625 s). No join was refused |

## N6: not measured

- The owner dropped the 24-hour run on 2026-10-24: "we don't need to run a 24 hour test, lets skip this test and move on to the next task." The phase requirements now list N6 as "dropped by the owner", and tasks 10.10 and 10.11 are gone.
- 10.10's 3-minute dry run had started, and was stopped. Its output is in `~/.run-phase/out/<folder>-2/n6-dry/`, and its half-built harness is in stash `run-phase/abandoned/board/10.10`. Neither is a result.
- So phase 2 has no figure for the board's stability or its heap over time. The longest the board ran here without a restart was 332 s, in N2.
- The desk checkpoint (2026-10-24, the test build) took two samples: 63,679 B of internal RAM free at boot (largest block 39,740 B), and 35,531 B (largest block 24,804 B) after 10 minutes, with the SSH link up. Nothing later was sampled; the owner watches for pager restarts on the acceptance day instead (the owner's notes, `~/.run-phase/out/phase-2-desk/checkpoint-notes.md`).
- What the day's bridge log shows of restarts is in §The desk checkpoint and the acceptance day: every reconnect on the day but one matches a flash, a restart or a change of network in the notes, and one session lasted 14 h 34 min overnight. That isn't a figure for N6: it has no heap sample, and the bridge's log can't tell a pager restart from a dropped link.

## The desk checkpoint and the acceptance day

Parsed by task 12.5, 2026-10-26.

- **The sources** are the owner's notes and the bridge's two logs, in `~/.run-phase/out/phase-2-desk/`, outside the repository:
  - `checkpoint-notes.md` and `bridge.log`: the desk checkpoint (task 12.3), 2026-10-24;
  - `day-notes.md` and `day-bridge.log`: the acceptance day (task 12.4), 2026-10-25. Its fixes and their re-test are in `.kiro/specs/phase-2-desk-pager/day-1-fixes.md`.
- **The clock:** the logs are on the Mac's clock, and the times below are as logged. The UK's clocks went back at 02:00 on 25 October, so 24 October's lines are at +01:00. 25 October's are in UTC, which was the owner's local time that day.
- **What the logs can show:** the bridge logs pane ids, verbs and seqs, never prompt text or agent names (parent 11.5). The notes tie each verdict to its prompt.

### The desk checkpoint (2026-10-24): PASS

- **The bridge:** `serve` from 07:35 (+01:00); the log's last line is at 13:45:42. It was started five times: the first binary, then one rebuild for each of the checkpoint's four changes to the bridge (design D2-14).
- **What it logged:**

| Line | Count | What |
|---|---|---|
| `verdict sent` | 10 | 8 approves, 1 deny and 1 interrupt, all on the throwaway session's pane `w4:pG` (an interrupt logged as a verdict then; since 2026-10-27 it logs `interrupt sent`: open-items O-17) |
| `verdict stale; nothing sent` | 1 | 11:41:19, an approve, `why=status`: step 3b-4 |
| `focused` (jump) | 9 | |
| `herdr call failed` | 0 | herdr's live shapes held (design D2-11, step 3e) |
| `key refused` | 2 | 07:59:52, the pairing whose last line the restart cut (fixed, `7e18ecf`); 12:07:23, after the revoke (step 3d-5) |
| `device no longer paired; session closed` | 1 | 12:07:21, the revoke (step 3d-5) |
| `an agent without a pane id …; left out` | 2,030 | all from the first binary, 07:35:24–07:42:09: pane ids with letters (fixed, `9c281eb`). None after |

- **The verdicts against the notes:**
  - 11:30:32, approve: step 3b-1. Step 3b-2's early release sent nothing.
  - 11:35:36, deny: step 3b-3.
  - 11:41:19, stale: step 3b-4 (`touch t4.txt`). On the first try (`t3`) the pager showed SCREEN CHANGED and the log has no verdict; `t3.txt` wasn't created.
  - 11:45:22, approve: step 3c, the 2 s hold on the danger card. Its 1 s and 1.5 s holds sent nothing.
  - 11:50:53, approve, then 11:51:21, interrupt: step 3d-2, the ping approved, then stopped.
  - 12:40:26, 12:41:24 and 12:42:06, approves: N2's three presses (step 4).
  - 13:45:42, approve: the release build back on, after the fix that makes a page select its session.
  - 10:50:30, approve: before step 3b's. The notes don't itemise it.
- **The smoke test** (step 2) ran in its own `pager-bridge stdio`, whose log went to the owner's window, not to `bridge.log`. The notes have its two results: `ok`, then `stale` for the identical second approve.
- **N2's three real presses:** the `verdict sent` times in the log match the notes' to the millisecond. The BOOT-down times come from the notes alone: the monitor's saved log (`log.herdr-pager.*.txt`) isn't in the folder.

| Press | BOOT down | Hold complete (+1 s) | `verdict sent` | Latency |
|---|---|---|---|---|
| n2-1 | 12:40:25.028 | 12:40:26.028 | 12:40:26.075 | 0.047 s |
| n2-2 | 12:41:23.722 | 12:41:24.722 | 12:41:24.766 | 0.044 s |
| n2-3 | 12:42:05.877 | 12:42:06.877 | 12:42:06.921 | 0.044 s |

### The acceptance day (2026-10-25): PASS

- **The bridge:** `day-bridge.log` runs from 13:52 on 24 October (+01:00), when the owner started the day's bridge after the checkpoint, to `serve: stopping` at 20:23:51 on 25 October. It was started once more, at 10:11:24, for the rebuilt binary (day 1's F2).
- **R3.2, the log against the notes: PASS.** No approve the owner didn't give, and no stale prompt approved. A sent verdict has passed the bridge's re-check of the status, seq and hash.

| On 25 October | The log | The owner's notes |
|---|---|---|
| Approves sent | 2: 10:17:56 (`w4:pG`, seq 948) and 11:35:14 (`w4:pG`, seq 976) | 2: 10:17 and 11:35, both in hp-check (`w4:pG`) |
| Denies and interrupts sent | 0 | 0 |
| `verdict stale` | 0 | no SCREEN CHANGED |

- **R3.1:** the owner recorded the day as passed (`38f5313`, 22:39), every permission prompt answered from the pager. Both of the log's verdicts were in the throwaway hp-check session, on prompts the session submitted for the re-test. The notes list no prompt answered at the keyboard.
- **Also in the log:**
  - 3 jumps: 07:53:58 to `wB:p1`, the finished turn of day 1's finding 2; then 10:21:55 and 10:21:59 to `w4:pG`, during re-test R1.
  - One `herdr call failed` (17:30:27, `show`, `code=unreachable`): re-test R6's show, read after the frozen bridge resumed, when its session had already closed.
- **R3.3:** each of the day's defects went RED before its fix (F1, F2, F5 and F6, in `RUNLOG.md`). F3 was a change to `test-wifi.toml` and a new pairing; F4 went to grilling item 21.

### Pager restarts

The checkpoint's two heap samples left the pager's memory past 10 minutes unknown, and N6 was dropped, so the owner watched for pager restarts on the day. The bridge logs two signs of one:
- `the device connected again; its older session closed`, when the pager connects while its older session is still open;
- `session: idle`, when a session has been silent for 3 minutes.

It can't tell a pager restart from a link the pager dropped.

- **25 October:** 9 reconnects. Eight match an event in the notes:
  - the flashes at 10:05:59 and 10:07:52 (S2), 11:54:59 (F5) and 15:22:30 (F6);
  - the restart over USB at 10:19:19 (R1);
  - the hotspot test's restarts at 11:30:33 and 11:34:00, and the move back to home Wi-Fi at 11:39:25 (R3).
  - The ninth, at 11:56:28, isn't in the notes. One candidate is R3's last step, a new pairing with home first, which restarts the pager; the notes don't time it.
- **24 October, after the checkpoint:** a reconnect at 13:59:17, a session closed as idle at 14:16:18 and a reconnect at 20:32:23 (+01:00). The notes don't explain them.
- **Overnight:** no line from 20:32:23 on 24 October (+01:00) to the flash at 10:05:59 on 25 October (UTC). So one session lasted 14 h 34 min; a pager restart in that time would have logged one of the two lines.
- **27 October, on the hotspot at the desk (O-77):** the owner's report of an empty screen on the iPhone's hotspot suggests a restart there, away from home on battery. At the desk, on USB power, the pager was paired with the hotspot alone and its serial log recorded for 11 minutes (21:05–21:16 UTC, `~/.run-phase/out/phase-2-desk/hotspot-desk.log`) while the owner pressed PWR and BOOT, held BOOT and let the screen sleep. The board's uptime ran unbroken to 658 s, with no crash, watchdog or brownout line. Every dial failed in about 7 s on `tcp: can't resolve the host`, with the waits between dials growing to about 30 s. Since O-77, the boot line and device info's STARTED say why the pager last started, so the next restart names its cause.

## Also from the board (not budgets)

- **The image:** the release build is `0x163420` bytes (1.39 MiB) of the `0x177000` factory partition (`SINGLE_APP_LARGE`), 5 % free (tasks 10.2 and 10.6).
- **Internal RAM at boot, before the link:** 60,991–67,043 B free, with a largest block of 31,744 B. The lowest free since reset was 39,740–40,296 B by the first `@L … heap` line. PSRAM: 8,066,212 B free. These figures come from the measurement runs' serial logs.
