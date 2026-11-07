# Goldens

The emulator harness (`bridge/emutest`, phase 2 design D2-8) compares screenshots with these PNGs, pixel by pixel outside masked rectangles (the clock, ages).

- `pairing/`: boot unpaired, pairing and the link's screens (lanes `fw` and `pairing`).
- `desk/`: the desk pager's screens (lane `goldens`).

A mismatch leaves `<name>_actual.png` and `<name>_diff.png` under `bridge/emutest/out/<test name>/` (gitignored), and the golden untouched. A missing golden is written from the screenshot and fails the test once: check it, then run again.

After an intended change to the desk screens, `PAGER_EMUTEST=1 go test ./emutest/ -update-goldens` (in `bridge/`) rewrites each desk golden the run reaches and fails: check them, then run again without it. It doesn't touch `pairing/`.
