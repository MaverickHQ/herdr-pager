# The desk pager is the MVP

The initiative's MVP is the desk pager. It runs on home Wi-Fi and USB power, pages within 6 seconds when a Claude Code agent is blocked, and takes approve and deny verdicts with the stale check, keeping every pocket-safety invariant. What the original specification called its MVP is split. Its personal features (hotspot use, battery life, touch navigation, snooze, quiet hours, the owner's own statistics) form the portable pager. Its publishing features (one-click install, published metrics, CI, the newcomer test) belong to the optional publish phase, whose first release is v1.0. lcd349-kit's own MVP is only what the desk pager needs: the kit working on the board, tagged locally and not published. The original "MVP" was really a first public release. The smallest thing that proves the product is worth having is a pager the owner relies on at the desk for real work; everything else builds on that proof (ADR 0001).

**Status:** accepted, 2026-10-18 (program-scope grilling).

## Consequences

- "MVP" is no longer used on its own. The docs say "desk pager", "kit MVP", "portable pager" or "v1.0" (see `GLOSSARY.md`).
- The desk pager also shows "ON BATTERY" when unplugged, so that it never fails silently; battery level and power-off stay with the portable pager.
- The original specification's acceptance criteria are unchanged; the phase specs say which ones each phase delivers (`.kiro/specs/phase-2-desk-pager/requirements.md`).
