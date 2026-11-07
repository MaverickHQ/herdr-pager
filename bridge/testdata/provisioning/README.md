# Provisioning fixtures (`@P` lines)

The pairing contract between `pager-bridge provision` (the tool) and the pager, over the board's USB serial console (phase 2 design D2-13, parent design §Provisioning): a valid and an invalid example of every `@P` line in both directions. They were written by hand on 2026-10-22 (phase 2, task 1.2). Three suites read them:

- `TestProvisioningFixturesParse` (`bridge/internal/provision`) parses every valid line and rejects every invalid one, at the key its row below names. It also checks that this README lists every file, and that every message type has both examples.
- Lane `cli`'s fake serial peer (seam S2-5) speaks them.
- The firmware's pairing tests (seam S2-6, task 8.2) feed them to the app core.

If the two sides disagree about a line, change the fixture and this README first, then both readers.

**Amended 2026-11-07** ([ADR 0011](../../../docs/adr/0011-pairing-sends-nothing-secret-before-both-fingerprints-are-confirmed.md), the owner's grilling of phase 5's requirement 2.9): pairing is two messages, a `prov_offer` with nothing secret, then a `prov_set` with the Wi-Fi networks, sent only once both fingerprints are confirmed. The contract below is the new one; phase 5's task 7.0e brought the fixtures, the tool and the firmware to it, and wrote §The device's states.

## Format

- `to-tool/` holds lines the device sends; `to-device/` lines the tool sends. Each has `valid/` and `invalid/`.
- Each `.txt` file is exactly one line ending in `\n`, with no other newline: the three bytes `@P `, then one JSON object. The JSON is compact except in the four boundary files, which are padded with spaces before their closing `}` to an exact length.
- **Every other line on the console is a log line**, which both sides ignore: only a line starting with exactly `@P ` (with its space) is a provisioning line.
- **Values are made up.** The keys are fresh ones whose private halves were thrown away; the Wi-Fi passwords are invented; the Mac is `studio-mac` and the phone `Sam’s iPhone`. Like phase 0's recon (`capture-claude.sh`'s privacy scan), the fixtures hold no login, host name or address of the owner's.

The fixture keys, and the full SHA256 fingerprints a test should expect to see printed or shown (parent 2.5):

| Key | Where | Fingerprint |
|---|---|---|
| The device's, `ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAnULfN18UYEHFPLj/PhfEMf7NvNdFoELzBJBtMsCVgj` | `pubkey` in `prov_hello` | `SHA256://a7FlX9Y4t0WSS+W8ANyY4w+5+axSEVbGzNXp6txjY` |
| The bridge's host key, `ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIITD0UXh5rvWrJ9ECHIk1KDIInpniagO0TKg6w/6c/tO` | `host_key` in `prov_offer` and `prov_set` | `SHA256:dsn3PXzY9otII88ZqYD/4URNANyDaFaYUI57pc+mMe0` |
| An ecdsa key | invalid examples only | `SHA256:nnZy+qtxcxg/Zh8yqXEPGC3UY7hc+v1MZG/RBhe62uk` |
| An rsa key (2,048 bits) | invalid examples only | `SHA256:o3TXuPnp8OkxHrFIee3K0AZZaPj6WxlM9Il/whlPBoU` |

## What a reader checks

### Framing (both directions)

- **Length:** a provisioning line is at most **512 bytes**, its `@P ` included and its `\n` not counted.
  - The tool builds `prov_offer` and `prov_set` before opening the port and refuses either if it would be longer: exit non-zero, nothing written, naming the limit. (The setup page has the networks only at its second step: it checks them then, and sends nothing on a refusal.) Four networks at WPA's longest (32-byte SSIDs, 63-character passphrases) don't fit in a `prov_set`; beside the bridge's ed25519 key, 389 bytes are left for the networks, so four typical ones do (`prov_set-4-networks.txt` is 344 bytes). The longest `prov_offer` the rules allow is about 320 bytes. The kit's console keeps lines up to 1,023 bytes (`LCD349_CONSOLE_LINE_MAX`), so the limit is the pager's own.
  - The device answers a longer line with `prov_result` `invalid`, as it does any line it can't read (§The device's states).
- **One object** after the prefix, with nothing after it but JSON whitespace.
- **No duplicate keys**, in the message or in any object a reader decodes inside it (each of `wifi`'s networks). Go keeps the last of two equal keys and cJSON the first.
- **Keys match exactly** (case-sensitive). **Unknown keys are ignored.**
- **`t`** is a string naming a message of the line's direction. A message of the other direction counts as unknown.

### The messages

Device → tool:

| `t` | Keys |
|---|---|
| `prov_hello` | `fw`, `pubkey`, `state` |
| `prov_result` | `ok`; `err` only when `ok` is false |

Tool → device:

| `t` | Keys |
|---|---|
| `prov_offer` | `name`, `host`, `port`, `host_key`, `host_name`: everything that isn't secret |
| `prov_set` | `host_key` (the confirmed offer's, exactly), `wifi` (1–4 networks, highest priority first, each `ssid` and `pass`) |

Every key listed is required (`err` only where noted). A missing key, a `null` or a value of the wrong JSON type is invalid. Lengths are in bytes after JSON decoding.

| Key | Rule |
|---|---|
| `fw` | 1–32 bytes of `0-9 A-Z a-z . + -` |
| `pubkey`, `host_key` | An OpenSSH public key, exactly `ssh-ed25519 <base64>` with no comment: the base64 decodes to the key type `ssh-ed25519` and a 32-byte key, with nothing after it (design D4). Any other type is refused with a precise error that names it (parent 2.4, phase 2 R4.2). |
| `state` | `waiting`, `confirming` or `confirmed` (below) |
| `ok` | A boolean |
| `err` | `cancelled`, `timeout`, `invalid`, `busy` or `storage` (below) |
| `name` | 1–24 bytes of `A-Z a-z 0-9 . _ -`, starting with a letter or digit: the device's name, the same rule as the pager protocol's `hello` |
| `wifi` | 1–4 networks (parent 16.1) |
| `ssid` | 1–32 bytes (802.11's limit). Any text: an iPhone's hotspot is named with a curly apostrophe (`Sam’s iPhone`, U+2019), so SSIDs aren't ASCII-only. |
| `pass` | Empty (an open network), or a WPA passphrase of 8–63 printable ASCII characters (0x20–0x7E), or exactly 64 hex digits (a raw key) |
| `host` | 1–64 bytes of `A-Z a-z 0-9 . : -`: the address the device dials, a `.local` name by default (`<LocalHostName>.local`, phase 2 R4.3) or an IP address |
| `port` | An integer from 1 to 65,535, written as plain digits |
| `host_name` | 1–64 bytes: the Mac's name, shown beside the fingerprints on the confirmation screen. Display text, so its content isn't checked; the device shows only printable ASCII. |

`state` in `prov_hello`:

| `state` | Meaning |
|---|---|
| `waiting` | In pairing mode, ready for a `prov_offer`. The tool sends `prov_offer` only after a `prov_hello` in this state. |
| `confirming` | Showing a `prov_offer`'s two fingerprints and host name, waiting for BOOT (pair) or PWR (cancel) |
| `confirmed` | BOOT was pressed on that offer: waiting up to 30 s for its `prov_set`. The tool sends `prov_set` only after a `prov_hello` in this state, and only once the owner has confirmed on the page or the terminal too. |

`err` in `prov_result`:

| `err` | When |
|---|---|
| `cancelled` | The owner pressed PWR on the confirmation screen (parent 2.7), or held the factory-reset chord there (the owner's decision, 2026-10-27, open-items O-28) |
| `timeout` | No button within 30 s of a `prov_offer` (parent 2.7), or no `prov_set` within 30 s of BOOT |
| `invalid` | A `prov_offer` or `prov_set` that breaks a rule above; a `prov_set` with no confirmed offer, or whose `host_key` isn't the confirmed offer's; any invalid `@P` line while waiting; or any line to a device with no usable key (it sends no `prov_hello`, so the tool never sends one) |
| `busy` | A `prov_offer` while another is being confirmed, or while the pager asks FACTORY RESET? |
| `storage` | BOOT was pressed, but saving the configuration failed |

On any `err`, the device keeps nothing from the offer or the `prov_set` (it never stores an offer), and the tool exits non-zero with `devices.toml` unchanged (parent 2.7). Only on `ok`, sent once the `prov_set` is accepted and saved, does the tool add the device (parent 2.6).

### The device's states

What a line does in each state of pairing mode (task 7.0e, from ADR 0011). "Ends the offer" means `prov_result` with that `err`, nothing kept, and back to `waiting`.

| State | A valid `prov_offer` | A `prov_set` | Any other line (an invalid `prov_offer`, broken JSON, a line over 512 bytes, another `t`) | BOOT | PWR |
|---|---|---|---|---|---|
| `waiting` | Shows it: `confirming` | `invalid` (no confirmed offer; an older bridge's one-message `prov_set` lands here) | `invalid` | nothing | nothing |
| `confirming` | `busy`; the offer shown stays | Ends the offer: `invalid` (no confirmed offer) | `busy` (an invalid `prov_offer` too); the offer shown stays | `confirmed`, and a `prov_hello` at once | Ends the offer: `cancelled` |
| `confirmed` | `busy`; the confirmed offer stays | Valid, with the offer's `host_key` exactly: saved, then `ok`. Anything else ends the offer: `invalid` | Ends the offer: `invalid` | nothing | Ends the offer: `cancelled` |

While the pager asks FACTORY RESET?, every line is `busy`. The chord on the confirmation screen, in `confirming` or `confirmed`, ends the offer: `cancelled`.

### Timing

| Who | Waits for | How long |
|---|---|---|
| Device | — | Sends `prov_hello` on entering pairing mode (an unprovisioned boot, after a factory reset, or **Pair again**), then every 2 s while in pairing mode, in any state; and at once on BOOT, in state `confirmed` |
| Tool | A `prov_hello` in state `waiting`, after opening the port | 120 s, telling the owner to choose **Pair again** on the device now; opening the port resets the board, so pairing mode is entered after it (phase 2 R4.4). With none it exits non-zero, and says to choose Pair again. |
| Device | BOOT or PWR, after a valid `prov_offer` | 30 s, then `prov_result` `timeout` (parent 2.6, 2.7) |
| Tool | A `prov_hello` in state `confirmed`, after sending `prov_offer` | 40 s: the device's 30 s, plus 10 s. With none it exits non-zero, nothing written and no password sent. |
| Tool | The owner's confirmation on the page, or `y` on `/dev/tty` for `pager-bridge provision` (`--yes` skips only this question) | as long as the device's 30 s below allow |
| Device | The `prov_set`, after BOOT | 30 s, then `prov_result` `timeout`, and back to `waiting` with nothing kept |
| Tool | `prov_result`, after sending `prov_set` | 40 s. With none it exits non-zero, nothing written. |

Outside pairing mode the device ignores `@P` lines (parent design §Provisioning: accepted only in provisioning mode).

Neither side ever prints or logs a Wi-Fi password, nor the device's private key, which never leaves the device (parent 2.8, phase 2 R4.5).

## The fixtures

Each invalid example is wrong in one key only, named under "Rejected at": `line` for its length, `json` for its syntax, `t` for its type, else the key's path (`wifi[0].pass`). The `t` column is the line's `t`, or `—` when it has none.

### Device → tool, valid

| File | `t` | Shows |
|---|---|---|
| `to-tool/valid/prov_hello.txt` | `prov_hello` | In pairing mode and waiting, with its ed25519 key |
| `to-tool/valid/prov_hello-confirming.txt` | `prov_hello` | Showing a `prov_offer`, waiting for BOOT or PWR |
| `to-tool/valid/prov_hello-confirmed.txt` | `prov_hello` | BOOT was pressed on the offer: waiting for its `prov_set` |
| `to-tool/valid/prov_hello-extra-field.txt` | `prov_hello` | An unknown key (`board`) is ignored |
| `to-tool/valid/prov_hello-512-bytes.txt` | `prov_hello` | A line of exactly 512 bytes, the limit |
| `to-tool/valid/prov_result-ok.txt` | `prov_result` | Paired: the tool now adds the device |
| `to-tool/valid/prov_result-cancelled.txt` | `prov_result` | `cancelled` |
| `to-tool/valid/prov_result-timeout.txt` | `prov_result` | `timeout` |
| `to-tool/valid/prov_result-invalid.txt` | `prov_result` | `invalid` |
| `to-tool/valid/prov_result-busy.txt` | `prov_result` | `busy` |
| `to-tool/valid/prov_result-storage.txt` | `prov_result` | `storage` |

### Device → tool, invalid

| File | `t` | Rejected at | Why |
|---|---|---|---|
| `to-tool/invalid/prov_hello-ecdsa-key.txt` | `prov_hello` | `pubkey` | An ecdsa key; device keys are ed25519 only |
| `to-tool/invalid/prov_hello-rsa-key.txt` | `prov_hello` | `pubkey` | An rsa key |
| `to-tool/invalid/prov_hello-truncated-key.txt` | `prov_hello` | `pubkey` | The key's last 8 base64 characters are missing, so it holds 26 of its 32 bytes |
| `to-tool/invalid/prov_hello-key-comment.txt` | `prov_hello` | `pubkey` | A comment after the key |
| `to-tool/invalid/prov_hello-duplicate-pubkey.txt` | `prov_hello` | `pubkey` | Two `pubkey` keys |
| `to-tool/invalid/prov_hello-bad-state.txt` | `prov_hello` | `state` | `paired` isn't a state |
| `to-tool/invalid/prov_hello-no-fw.txt` | `prov_hello` | `fw` | No `fw` |
| `to-tool/invalid/prov_hello-513-bytes.txt` | `prov_hello` | `line` | 513 bytes |
| `to-tool/invalid/prov_result-error-without-err.txt` | `prov_result` | `err` | `ok: false` without `err` |
| `to-tool/invalid/prov_result-ok-with-err.txt` | `prov_result` | `err` | `ok: true` with `err` |
| `to-tool/invalid/prov_result-unknown-err.txt` | `prov_result` | `err` | `wifi_failed` isn't an error |
| `to-tool/invalid/unknown-type.txt` | `prov_info` | `t` | No such message (the parent plan's old `prov_info`) |
| `to-tool/invalid/tool-type.txt` | `prov_set` | `t` | A tool message, sent by the device |
| `to-tool/invalid/malformed.txt` | — | `json` | Cut off after `fw` |

### Tool → device, valid

| File | `t` | Shows |
|---|---|---|
| `to-device/valid/prov_offer.txt` | `prov_offer` | pager-01, the default `.local` host |
| `to-device/valid/prov_offer-ip-host.txt` | `prov_offer` | `--host 127.0.0.1` and another port, as the emulator harness provisions |
| `to-device/valid/prov_set.txt` | `prov_set` | One network, with `prov_offer.txt`'s host key |
| `to-device/valid/prov_set-4-networks.txt` | `prov_set` | Four networks in priority order: a passphrase, an iPhone hotspot (a non-ASCII SSID), a 64-hex-digit key and an open network |
| `to-device/valid/prov_set-emu.txt` | `prov_set` | The emulator's simulated network, as the emulator harness provisions |
| `to-device/valid/prov_set-other-host-key.txt` | `prov_set` | Valid in form, but with another key than the offers' (the device's own): after an offer of the bridge's key, the device answers `invalid` |
| `to-device/valid/prov_set-512-bytes.txt` | `prov_set` | A line of exactly 512 bytes, the limit |

### Tool → device, invalid

| File | `t` | Rejected at | Why |
|---|---|---|---|
| `to-device/invalid/prov_offer-ecdsa-host-key.txt` | `prov_offer` | `host_key` | An ecdsa host key |
| `to-device/invalid/prov_offer-port-zero.txt` | `prov_offer` | `port` | Port 0 |
| `to-device/invalid/prov_offer-bad-name.txt` | `prov_offer` | `name` | `pager 01` has a space |
| `to-device/invalid/prov_offer-bad-host.txt` | `prov_offer` | `host` | `studio mac.local` has a space |
| `to-device/invalid/prov_offer-no-host-name.txt` | `prov_offer` | `host_name` | No `host_name` |
| `to-device/invalid/prov_set-no-host-key.txt` | `prov_set` | `host_key` | No `host_key` |
| `to-device/invalid/prov_set-no-networks.txt` | `prov_set` | `wifi` | No networks |
| `to-device/invalid/prov_set-5-networks.txt` | `prov_set` | `wifi` | Five networks; at most 4 |
| `to-device/invalid/prov_set-short-password.txt` | `prov_set` | `wifi[0].pass` | 7 characters; a passphrase has at least 8 |
| `to-device/invalid/prov_set-password-not-ascii.txt` | `prov_set` | `wifi[0].pass` | A passphrase with `ä` and `ü` |
| `to-device/invalid/prov_set-ssid-too-long.txt` | `prov_set` | `wifi[0].ssid` | A 33-byte SSID |
| `to-device/invalid/prov_set-duplicate-pass.txt` | `prov_set` | `wifi[0].pass` | Two `pass` keys in one network |
| `to-device/invalid/prov_set-ecdsa-host-key.txt` | `prov_set` | `host_key` | An ecdsa host key |
| `to-device/invalid/prov_set-513-bytes.txt` | `prov_set` | `line` | 513 bytes |
| `to-device/invalid/device-type.txt` | `prov_result` | `t` | A device message, sent by the tool |
| `to-device/invalid/malformed.txt` | — | `json` | Cut off inside `wifi` |
