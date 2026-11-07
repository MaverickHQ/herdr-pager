# No tailnet on the pager

herdr-pager doesn't join a tailnet (Tailscale, Headscale or plain WireGuard). It reaches the bridge only on networks it shares with the owner's Mac: home or office Wi-Fi, or a phone hotspot that the Mac has also joined, if hotspot clients can reach each other. A tailnet would serve one scenario: agents running on a Mac left at home while the owner is out with the pager and without the laptop. The owner's phone already covers that case better, with SSH apps over Tailscale (see the prior-art report).

The cost would have been:
- a whole phase;
- a second board, because the tailnet credential required flash encryption and secure boot, which permanently change the chip;
- an immature device client (MicroLink: 9 commits, no releases, "use at your own risk") needing about 116 KB of internal RAM the firmware doesn't have;
- a 3-second network heartbeat that would defeat light sleep;
- a conflict with the non-goal "no cloud relay, accounts or shared tokens".

**Status:** accepted, 2026-10-18.

## Consequences

- Parent requirement 17 (tailnet), budget N10 and the tailnet part of parent task 0.5 are dropped.
- Phase 4 ("on the go") is dropped. Later phases keep their numbers (5 publish, 6 platform growth), so there is no phase 4.
- Flash encryption, NVS encryption and secure boot are no longer required: they were a prerequisite only for storing a tailnet credential (parent 17.4). The lost-pager risk stays as the parent threat model accepts it (T3): physical access can read the device key and Wi-Fi passwords; `devices revoke` and changing the Wi-Fi password close it.
- No second board is needed.
- The vibration motor (parent requirement 14) doesn't depend on the tailnet. It becomes an optional part of the portable pager (phase 3).
- If on-the-go use away from a shared network is wanted later, the phone is the tool. A device tailnet would need a new grilling and would bring back everything above.
