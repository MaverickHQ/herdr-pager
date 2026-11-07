# herdr-pager is a personal tool first

herdr-pager is built for its owner first. Publishing (a public repository, releases, one-click install, a public lcd349-kit) happens only after the desk pager has proved itself in daily use, and only with the owner's explicit approval at that moment; nothing leaves the owner's Mac otherwise. The v1, voice messages over SSH, was archived for having no user. Building public scaffolding before the core loop is proven would repeat that mistake, while a working personal tool keeps its value whether or not it is ever published.

**Status:** accepted, 2026-10-18 (program-scope grilling). Superseded in part by ADR 0008 (2026-10-30): the desk pager is published before the portable pager.

## Consequences

- Phases are ordered by personal value. The portable pager (phase 3) comes before any publishing (superseded by ADR 0008: phase 5 now comes first). (A phase 4 for on-the-go use was dropped: ADR 0005.)
- Publishing is its own optional phase (5), started only by the owner's decision: lcd349-kit's public release first, then herdr-pager's v1.0.
- Each milestone is gated by real use: the portable pager starts only after 7 days in use of the desk pager (after its acceptance day; see `GLOSSARY.md`). A bug doesn't reset the count; only the owner choosing to stop using the pager stops the plan, for a re-grill.
- lcd349-kit's platform growth isn't gated this way: it is the owner's call, at any time and in any order.

## Considered options

- **Public product from the start.** Rejected: it commits effort to install, metrics and release work before anyone, including the owner, has used the pager.
- **Personal only, never published.** Rejected for now: publishing stays an option the owner can take later.
