# Pager protocol v1 fixtures

The contract between the bridge and the pager (phase 2 design D2-6): a valid and an invalid example of every message in parent design §Protocol, in both directions. They were written by hand on 2026-10-22 (phase 2, task 1.1). Three suites read them:

- `TestProtocolFixturesParse` (`bridge/internal/protocol`) parses every valid line and rejects every invalid one, at the key its row below names. It also checks that this README lists every file, and that every message type has both examples.
- The bridge's end-to-end tests (seam S2-1) take their message shapes from them.
- The firmware's codec tests (seam S2-6) feed the `to-device` lines to the app core, and compare each line the core writes (`hello`, `list`, `show`, `approve`, `deny`, `interrupt`, `jump`) with its `to-bridge/valid` file, byte for byte but for the `id` (open-items O-39). The `ping` is the SSH link's, not the core's: the emulator suite fails any test in which the bridge logged `session: invalid message`, so every line the running pager sends is checked there.

If the two sides disagree about a line, change the fixture and this README first, then both readers.

## Format

- `to-bridge/` holds lines the device sends; `to-device/` lines the bridge sends. Each has `valid/` and `invalid/`.
- Each `.ndjson` file is exactly one line ending in `\n`, with no other newline. The JSON is compact except in the four boundary files, which are padded with spaces before their closing `}` to an exact length.
- **Values are illustrative.** The cards' text follows phase 0's captures (`bridge/testdata/herdr/claude/`), but lane `cards` takes its expected values from those captures through the manifest (task 1.3), never from these lines. The hashes are well formed but made up: only the bridge computes a card's hash (parent design §Bridge components), so no test should recompute one from these files.

## What a reader checks

### Framing (both directions)

- **Length:** a line is at most 512 bytes from the device and 8,192 bytes from the bridge, not counting its `\n` (parent 13.5). The bridge closes a session that sends a longer line; the device drops a longer line and sends `list`.
- **One object:** a line is exactly one JSON object, with nothing before or after it but JSON whitespace.
- **No duplicate keys** in the message or in any object a reader decodes inside it (a snapshot's agents). Go keeps the last of two equal keys and cJSON the first, so allowing them would let the two sides read different messages from one line.
- **Keys match exactly** (case-sensitive). **Unknown keys are ignored**: the bridge never acts on them (an `approve` carrying `keys` still sends only the keymap's keys).
- **`t`** is a string naming a message of the line's direction. A message of the other direction counts as unknown.

### Values

- **Required keys:** every key in the message's shape below, except `risk`, `rl` and `err`, which appear only where noted, and a snapshot entry's `at`, which may be left out. A missing key, a `null` or a value of the wrong JSON type is invalid.
- **Integers** are plain decimal digits: no fraction, exponent or `+`, and a leading `-` only where a negative value is allowed.
  - `id`: 0 to 4,294,967,295 (it fits a `uint32_t`).
  - `seq`, `dseq`, `since`, `now`, `more`: 0 to 2^53 − 1 (9,007,199,254,740,991), so that every JSON reader holds them exactly.
  - `pg`: −1 to 2^53 − 1 (−1: the agent never paged).
  - `v`: exactly 1.
- **`pane`:** herdr's full pane id, `w<id>:p<id>`, at most 32 bytes, where each id is ASCII digits and letters: herdr counts past 9 with letters (`w1:p3`, `w6:pE`, `wA:p1`; seen live on 2026-10-24, the desk checkpoint). Never a short form such as `p3`.
- **`at`** (a snapshot entry, optional): where the agent works, as the device shows it beside the name: its folder's name, 0 to 16 printable ASCII characters (added 2026-10-24, at the desk checkpoint). Absent or empty, the device shows the kind there.
- **`hash`:** 64 lowercase hex digits.
- **`st`:** `idle`, `working`, `blocked`, `done` or `unknown`.
- **`can`:** an array of verbs: `show`, `approve`, `deny`, `interrupt`, `jump`.

### Text

Lengths are in bytes after JSON decoding.

| Message | Key | Bytes | Characters |
|---|---|---|---|
| device `hello` | `fw` | 1–32 | `0-9 A-Z a-z . + -` |
| device `hello` | `name` | 1–24 | `A-Z a-z 0-9 . _ -`, starting with a letter or digit (the same rule as provisioning's `name`) |
| bridge `hello` | `bridge` | 1–32 | `0-9 A-Z a-z . + -` |
| bridge `hello` | `host` | 1–64 | display text |
| `snap` entry | `name` | 1–24 | display text |
| `snap` entry | `kind` | 1–10 | display text |
| `card` | `summary` | 0–40 | display text |
| `card` | each of `lines` | 0–40 | display text |
| `card` | `rl` | 1–32 | display text (the banner `DANGER: <label>` fills at most the 40-column row) |

The bridge sanitizes display text before sending it (printable ASCII, parent 13.4), but a reader doesn't reject other bytes: the device replaces any byte outside 0x20–0x7E with `?` before drawing it (parent design §Sanitizer; task 6.40 writes its own inputs). A `\u0000` escape decodes to a NUL byte, so a C reader must keep the decoded length rather than stop at the NUL.

### The messages

Device → bridge (the complete allowlist, parent 13.1):

| `t` | Keys |
|---|---|
| `hello` | `v`, `fw`, `name`, `lines` (optional) |
| `ping` | `id` |
| `list` | `id` |
| `show` | `id`, `pane`, `pg` |
| `approve` | `id`, `pane`, `seq`, `hash` |
| `deny` | `id`, `pane`, `seq`, `hash` |
| `interrupt` | `id`, `pane`, `seq` |
| `jump` | `id`, `pane` |

Bridge → device:

| `t` | Keys |
|---|---|
| `hello` | `v`, `bridge`, `host`, `now` |
| `snap` | `now`, `herdr`, `more`, `agents`; each entry `pane`, `name`, `kind`, `st`, `seq`, `dseq`, `since`, `can`, and optionally `at` |
| `card` | `id`, `pane`, `st`, `seq`, `hash`, `kind`, `summary`, `lines`, `can`; `risk` and `rl` on a danger card only |
| `result` | `id`, `ok`; `err` only when `ok` is false |
| `pong` | `id`, `now` |

**The device ignores the bridge's `hello` and `pong`**, valid or not: it reads only `snap`, `card` and `result`. A session is live once SSH connects; a `pong`'s bytes, like any others, only keep the SSH link's 150 s silence check from firing. So their invalid examples are for the bridge's reader and encoder; the firmware's tests skip them by name and check that a valid `hello` or `pong` changes nothing (the owner's decision, 2026-10-27, open-items O-39).

Rules across keys:

- **`snap`:** `herdr` is `ok` or `offline`, and when offline `agents` is empty and `more` is 0. At most 16 agents; `more` is above 0 only when there are 16. No pane appears twice.
- **A snapshot entry's `can`** never holds `approve` or `deny`: only a card offers them (parent 8.1, 8.6). It holds `interrupt` only when `st` is `working` (phase 2 R6.3).
- **The device's `hello`:** `lines`, if present, is 3 or 4: the most lines of a card the pager draws (added 2026-11-02, O-90's second review). Without it the bridge takes 3, a pager from before then, and sends it no card of more than 3 lines: an MCP card that needs 4 fails closed (TOOL NOT READ, deny only). A bridge from before then ignores `lines`, as it ignores any key it doesn't know, and sends no card of more than 3 lines, so a new pager works with it; `v` stays 1, since such a bridge refuses any other. Update the pager and the bridge together all the same: an old pager drops a card of 4 lines.
- **`card`:** `kind` is `permission`, `question` or `status`. `lines` holds at most 4 (3 until 2026-11-02: an MCP card shows its tool and up to 3 arguments, O-90). `risk` and `rl` come together, and `risk` is `danger`. `can` holds `approve` and `deny` only on a permission card and `interrupt` only when `st` is `working`; a question card's `can` is exactly `["jump"]` (phase 2 R6.3, parent 7.7). A permission card that failed closed offers `deny` without `approve`; the device then offers deny alone (added 2026-11-01, the security pass).
- **`result`:** `ok` is a boolean. When it's true there is no `err`; when it's false, `err` is one of `stale`, `not_allowed`, `unknown_pane`, `herdr_offline`, `herdr_error`, `bad_request`, `rate_limited`.

A reader doesn't check:
- the order of a snapshot's agents (the device sorts them by its own seen state, task 6.1);
- whether `since` is before `now`;
- whether a hash matches its card (only the bridge recomputes it, parent 8.3);
- whether an `id` echoes a request.

## The fixtures

Each invalid example is wrong in one key only, named under "Rejected at": `line` for its length, `json` for its syntax, `t` for its type, else the key's path (`agents[0].can`, `lines[0]`). The `t` column is the line's `t`, or `—` when it has none.

### Device → bridge, valid

| File | `t` | Shows |
|---|---|---|
| `to-bridge/valid/hello.ndjson` | `hello` | The device's first line on a new session; `lines` 4: it draws a card of 4 lines (added 2026-11-02, O-90's second review) |
| `to-bridge/valid/hello-three-lines.ndjson` | `hello` | A pager from before `lines`: no `lines`, so the bridge sends it cards of 3 lines |
| `to-bridge/valid/ping.ndjson` | `ping` | The keepalive the device sends every 60 s |
| `to-bridge/valid/ping-max-id.ndjson` | `ping` | The largest `id`, 4,294,967,295 |
| `to-bridge/valid/ping-512-bytes.ndjson` | `ping` | A line of exactly 512 bytes, the device's limit |
| `to-bridge/valid/list.ndjson` | `list` | A request for a fresh snapshot |
| `to-bridge/valid/show.ndjson` | `show` | Open the card of `w1:p3`, 4.2 s after its page |
| `to-bridge/valid/show-letter-pane.ndjson` | `show` | A pane id with a letter, `w6:pE`, as herdr gives past 9 |
| `to-bridge/valid/show-never-paged.ndjson` | `show` | `pg` −1: a card for an agent that never paged |
| `to-bridge/valid/approve.ndjson` | `approve` | Approve `card-permission`'s card, by its seq and hash |
| `to-bridge/valid/approve-extra-field.ndjson` | `approve` | An unknown key (`keys`) is ignored; the bridge still sends only the keymap's keys |
| `to-bridge/valid/deny.ndjson` | `deny` | Deny the same card |
| `to-bridge/valid/interrupt.ndjson` | `interrupt` | Interrupt the working agent `w3:p1` |
| `to-bridge/valid/jump.ndjson` | `jump` | Focus `w1:p3` in herdr |

### Device → bridge, invalid

| File | `t` | Rejected at | Why |
|---|---|---|---|
| `to-bridge/invalid/hello-version-2.ndjson` | `hello` | `v` | Protocol version 2; only 1 exists |
| `to-bridge/invalid/hello-no-name.ndjson` | `hello` | `name` | No `name` |
| `to-bridge/invalid/hello-name-too-long.ndjson` | `hello` | `name` | A 25-byte name; at most 24 |
| `to-bridge/invalid/hello-lines-5.ndjson` | `hello` | `lines` | 5 lines; 3 or 4 |
| `to-bridge/invalid/ping-id-string.ndjson` | `ping` | `id` | `"7"` is a string, not an integer |
| `to-bridge/invalid/ping-id-too-big.ndjson` | `ping` | `id` | 4,294,967,296 doesn't fit 32 bits |
| `to-bridge/invalid/ping-513-bytes.ndjson` | `ping` | `line` | 513 bytes: the bridge closes the session |
| `to-bridge/invalid/list-no-id.ndjson` | `list` | `id` | No `id` |
| `to-bridge/invalid/show-short-pane.ndjson` | `show` | `pane` | `p3` is a short form; panes are `w1:p3` |
| `to-bridge/invalid/show-pane-punctuation.ndjson` | `show` | `pane` | `w6:p-E`: an id is digits and letters only |
| `to-bridge/invalid/show-pg-below-minus-one.ndjson` | `show` | `pg` | −2; `pg` is −1 or more |
| `to-bridge/invalid/approve-short-hash.ndjson` | `approve` | `hash` | 63 hex digits |
| `to-bridge/invalid/approve-uppercase-hash.ndjson` | `approve` | `hash` | Uppercase hex |
| `to-bridge/invalid/approve-no-seq.ndjson` | `approve` | `seq` | No `seq` |
| `to-bridge/invalid/approve-seq-fraction.ndjson` | `approve` | `seq` | `41.0` isn't written as an integer |
| `to-bridge/invalid/approve-duplicate-hash.ndjson` | `approve` | `hash` | Two `hash` keys |
| `to-bridge/invalid/deny-no-hash.ndjson` | `deny` | `hash` | No `hash` |
| `to-bridge/invalid/interrupt-no-seq.ndjson` | `interrupt` | `seq` | No `seq` |
| `to-bridge/invalid/jump-no-pane.ndjson` | `jump` | `pane` | No `pane` |
| `to-bridge/invalid/unknown-type.ndjson` | `send_text` | `t` | Not in the allowlist (parent 13.1): the device can never send text |
| `to-bridge/invalid/bridge-type.ndjson` | `result` | `t` | A bridge message, sent by the device |
| `to-bridge/invalid/no-type.ndjson` | — | `t` | No `t` |
| `to-bridge/invalid/malformed.ndjson` | — | `json` | Cut off before its closing `}` |
| `to-bridge/invalid/not-an-object.ndjson` | — | `json` | An array, not an object |
| `to-bridge/invalid/trailing-data.ndjson` | — | `json` | Two objects on one line |

### Bridge → device, valid

| File | `t` | Shows |
|---|---|---|
| `to-device/valid/hello.ndjson` | `hello` | The bridge's reply to the device's hello |
| `to-device/valid/snap.ndjson` | `snap` | One blocked agent (parent design §Protocol's example) |
| `to-device/valid/snap-letter-pane.ndjson` | `snap` | One blocked agent in pane `wA:pN`, ids with letters |
| `to-device/valid/snap-folder.ndjson` | `snap` | One blocked agent named by its title, working in `herdr-pager` (`at`) |
| `to-device/valid/snap-16-agents-more.ndjson` | `snap` | 16 agents and `more: 4`: blocked, done, unknown, working and idle; names that fall back to `<kind> <pane id>`; `codex`, a kind with no keymap, so no `interrupt`; a 24-byte name, truncated head and tail |
| `to-device/valid/snap-empty.ndjson` | `snap` | herdr is up and has no agents |
| `to-device/valid/snap-offline.ndjson` | `snap` | herdr is offline |
| `to-device/valid/snap-8192-bytes.ndjson` | `snap` | A line of exactly 8,192 bytes, the device's limit |
| `to-device/valid/card-permission.ndjson` | `card` | A permission card from phase 0's shell capture: the command first |
| `to-device/valid/card-permission-file.ndjson` | `card` | A file-edit permission card from phase 0's capture: the path first |
| `to-device/valid/card-danger.ndjson` | `card` | A danger card from phase 0's force-push capture, with `risk` and `rl` |
| `to-device/valid/card-permission-unread.ndjson` | `card` | A permission card that failed closed (phase 0's shell capture with a `╌` in its command): COMMAND NOT READ first, deny and jump only (added 2026-11-01, the security pass) |
| `to-device/valid/card-mcp.ndjson` | `card` | An MCP call's card: its server and tool, then one line per argument, 4 lines in all (added 2026-11-02, O-90) |
| `to-device/valid/card-question.ndjson` | `card` | A question card from phase 0's question capture: jump only |
| `to-device/valid/card-escapes.ndjson` | `card` | JSON escapes as the bridge's encoder writes them: `&` and `>` as `\u0026` and `\u003e`, a quote as `\"`, a backslash as `\\`. The bridge's `Encode` gives these bytes back from what it parses; the device shows `make build && make test` and `echo "done" > C:\tmp\log` |
| `to-device/valid/card-status.ndjson` | `card` | A working agent's card, which can be interrupted |
| `to-device/valid/card-status-done.ndjson` | `card` | A done agent's card |
| `to-device/valid/result-ok.ndjson` | `result` | The verdict was sent |
| `to-device/valid/result-stale.ndjson` | `result` | `stale`: the screen changed, nothing was sent |
| `to-device/valid/result-not_allowed.ndjson` | `result` | `not_allowed` |
| `to-device/valid/result-unknown_pane.ndjson` | `result` | `unknown_pane` |
| `to-device/valid/result-herdr_offline.ndjson` | `result` | `herdr_offline` |
| `to-device/valid/result-herdr_error.ndjson` | `result` | `herdr_error`, the error the device shows as `X` |
| `to-device/valid/result-bad_request.ndjson` | `result` | `bad_request` |
| `to-device/valid/result-rate_limited.ndjson` | `result` | `rate_limited` |
| `to-device/valid/pong.ndjson` | `pong` | The reply to `ping`, with the bridge's time |
| `to-device/valid/pong-extra-field.ndjson` | `pong` | An unknown key (`uptime`) is ignored |

### Bridge → device, invalid

| File | `t` | Rejected at | Why |
|---|---|---|---|
| `to-device/invalid/hello-version-2.ndjson` | `hello` | `v` | Protocol version 2 |
| `to-device/invalid/hello-no-now.ndjson` | `hello` | `now` | No `now` |
| `to-device/invalid/snap-17-agents.ndjson` | `snap` | `agents` | 17 agents; at most 16 |
| `to-device/invalid/snap-more-without-16.ndjson` | `snap` | `more` | `more: 3` with only one agent |
| `to-device/invalid/snap-offline-with-agents.ndjson` | `snap` | `agents` | herdr offline, yet an agent |
| `to-device/invalid/snap-bad-herdr.ndjson` | `snap` | `herdr` | `error`; herdr is `ok` or `offline` |
| `to-device/invalid/snap-can-approve.ndjson` | `snap` | `agents[0].can` | A snapshot offering `approve` |
| `to-device/invalid/snap-interrupt-not-working.ndjson` | `snap` | `agents[0].can` | `interrupt` on a blocked agent |
| `to-device/invalid/snap-unknown-verb.ndjson` | `snap` | `agents[0].can` | `send_text` isn't a verb |
| `to-device/invalid/snap-at-too-long.ndjson` | `snap` | `agents[0].at` | `at` of 19 characters; at most 16 |
| `to-device/invalid/snap-short-pane.ndjson` | `snap` | `agents[0].pane` | `p3` is a short form |
| `to-device/invalid/snap-duplicate-pane.ndjson` | `snap` | `agents[1].pane` | `w1:p3` twice |
| `to-device/invalid/snap-name-too-long.ndjson` | `snap` | `agents[0].name` | A 25-byte name |
| `to-device/invalid/snap-empty-name.ndjson` | `snap` | `agents[0].name` | An empty name; at least 1 byte |
| `to-device/invalid/snap-kind-too-long.ndjson` | `snap` | `agents[0].kind` | An 11-byte kind |
| `to-device/invalid/snap-empty-kind.ndjson` | `snap` | `agents[0].kind` | An empty kind; at least 1 byte |
| `to-device/invalid/snap-bad-status.ndjson` | `snap` | `agents[0].st` | `stuck` isn't a status |
| `to-device/invalid/snap-seq-negative.ndjson` | `snap` | `agents[0].seq` | −1 |
| `to-device/invalid/snap-seq-minus-zero.ndjson` | `snap` | `agents[0].seq` | `-0`: a leading `-` only on a negative value |
| `to-device/invalid/snap-8193-bytes.ndjson` | `snap` | `line` | 8,193 bytes: the device drops it and sends `list` |
| `to-device/invalid/card-question-can-approve.ndjson` | `card` | `can` | A question card offering `approve` and `deny` |
| `to-device/invalid/card-question-can-jump-twice.ndjson` | `card` | `can` | `["jump","jump"]`: a question card's `can` is exactly `["jump"]` |
| `to-device/invalid/card-status-can-deny.ndjson` | `card` | `can` | A status card offering `deny` |
| `to-device/invalid/card-permission-can-interrupt.ndjson` | `card` | `can` | `interrupt` on a blocked agent's card |
| `to-device/invalid/card-five-lines.ndjson` | `card` | `lines` | 5 lines; at most 4 (amended 2026-11-02, O-90: an MCP card's tool and 3 arguments) |
| `to-device/invalid/card-line-too-long.ndjson` | `card` | `lines[0]` | A 41-byte line |
| `to-device/invalid/card-summary-too-long.ndjson` | `card` | `summary` | A 41-byte summary |
| `to-device/invalid/card-risk-without-label.ndjson` | `card` | `rl` | `risk` without `rl` |
| `to-device/invalid/card-bad-risk.ndjson` | `card` | `risk` | `high`; the only risk is `danger` |
| `to-device/invalid/card-label-too-long.ndjson` | `card` | `rl` | A 33-byte label |
| `to-device/invalid/card-bad-kind.ndjson` | `card` | `kind` | `danger` isn't a kind (danger is `risk`) |
| `to-device/invalid/card-no-hash.ndjson` | `card` | `hash` | No `hash` |
| `to-device/invalid/card-no-id.ndjson` | `card` | `id` | No `id`: a card only ever answers a `show` |
| `to-device/invalid/card-duplicate-summary.ndjson` | `card` | `summary` | Two `summary` keys |
| `to-device/invalid/result-error-without-err.ndjson` | `result` | `err` | `ok: false` without `err` |
| `to-device/invalid/result-ok-with-err.ndjson` | `result` | `err` | `ok: true` with `err` |
| `to-device/invalid/result-unknown-err.ndjson` | `result` | `err` | `timeout` isn't an error the protocol has |
| `to-device/invalid/result-ok-not-bool.ndjson` | `result` | `ok` | `"true"` is a string |
| `to-device/invalid/pong-no-now.ndjson` | `pong` | `now` | No `now` |
| `to-device/invalid/unknown-type.ndjson` | `frame` | `t` | No such message: terminal text reaches the device only in a card (parent 4.5) |
| `to-device/invalid/device-type.ndjson` | `approve` | `t` | A device message, sent by the bridge |
| `to-device/invalid/malformed.ndjson` | — | `json` | Cut off inside `agents` |
