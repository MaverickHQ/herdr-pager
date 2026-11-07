# The pager drives the kit's emulator from its own Go harness

herdr-pager's emulator tests run through its own Go harness, `bridge/emutest`, not through the kit's scenario files. The desk pager's flows need things the kit's scenarios can't do. A scenario can press buttons, wait for logs, take screenshots and set state. It can't start and stop host processes (a real `pager-bridge serve` and the fake herdr), write to the emulated board's serial port (pairing), or seed storage. The harness drives the kit's `lcd349` CLI for the emulator, and runs the bridge and the fake herdr beside it. It pairs over the instance's virtual serial port, and compares screenshots with goldens using a comparator of its own, which masks the clock and ages. Because it lives in the bridge's Go module, it uses the fake herdr's package directly. The alternative was new scenario steps in the kit (processes, serial send and expect, storage), which would serve other products too, but no kit phase builds them.

**Status:** accepted, 2026-10-22 (phase 2's grilling, item 16).

## Consequences

- The pager's emulator tests depend only on the kit's CLI, which is a public, documented surface, not on the kit's internal packages. The kit's golden comparator can't be imported from another module anyway.
- The harness's tests skip unless `PAGER_EMUTEST=1` is set, so the bridge's ordinary test run doesn't need the firmware built.
- Goldens live in `firmware/scenarios/`. A golden mismatch shows up in the bridge's test output, not in `lcd349 scenario`.
- If the kit later gains process and serial steps (phase 6), the pager may move its flows there. Nothing here blocks that.
- The fake herdr has to be a runnable program as well as a package (`bridge/cmd/fakeherdr`), for the measurements on the board.
