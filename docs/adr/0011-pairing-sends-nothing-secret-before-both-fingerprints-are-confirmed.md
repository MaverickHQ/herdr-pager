# Pairing sends nothing secret before both fingerprints are confirmed

Pairing is two messages, not one. The tool first sends a `prov_offer` with everything that isn't secret: the device's name, the address and port it will dial, the bridge's host key and the Mac's name. The pager shows both full fingerprints and the Mac's name, and the owner presses BOOT. Only then, and only after the owner has also confirmed on the setup page (or, with `pager-bridge provision`, on the terminal) that the fingerprints match, does the tool send `prov_set`, which carries the Wi-Fi networks and their passwords and the same host key. The pager takes it without a second press if the key matches the offer it confirmed and it arrives within 30 s of BOOT. Before this, a single `prov_set` carried the passwords with the host key, so whatever answered on the USB port received them before anyone compared a fingerprint (open-items O-80, finding 9).

**Status:** accepted, 2026-11-07 (the owner's grilling of phase 5's requirement 2.9, task 7.0d).

## Consequences

- The provisioning contract (`bridge/testdata/provisioning/README.md`) gains `prov_offer`, a `confirmed` state in `prov_hello`, and a `prov_set` that carries only `host_key` and `wifi`. A `prov_set` with no confirmed offer, or with another host key, is `invalid` and keeps nothing.
- Two confirmations: BOOT on the pager first (the device's own trust decision, which nothing on the Mac can make for it), then the owner's on the page or the terminal. The page's one button, "They match: send the Wi-Fi networks", is that second confirmation.
- `pager-bridge provision` asks "Do these match the pager's screen? [y/N]" on `/dev/tty` (its stdin carries the passwords) once the pager reports `confirmed`. `--yes` skips only that question, never BOOT, for the board checks and the emulator tests that run it unattended.
- No fallback in either direction: a pager with older firmware answers the offer `invalid`, and the tool and the page say to flash the firmware shipped with the bridge first; a pager with this firmware refuses an older bridge's bare `prov_set`. The pager and the bridge are updated together.
- The pager still keeps nothing until `prov_set` is accepted and saved; it never stores an offer.

## Considered options

- **PAIR VIA USB shows the pager's own fingerprint, compared on the page before a single `prov_set`.** No new message and a smaller firmware change, but the bridge's fingerprint would still be confirmed on the pager only after the passwords had arrived: half of requirement 2.9. Rejected.
- **A new device-to-tool message for the confirmation.** Rejected: the `prov_hello` the pager already sends every 2 s carries the new `confirmed` state just as well.
- **Keep the one message, and rely on the owner choosing the right USB port.** Rejected: the passwords would go to any device that answers `prov_hello` first.
