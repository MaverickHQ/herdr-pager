# Publish the desk pager before the portable pager

herdr-pager's first published version, v1.0, is the desk pager, published before the portable pager (phase 3) is built. The desk pager passed its acceptance day, the review's fixes and a field test on a phone hotspot, and its owner uses it daily; that is the proof ADR 0001 asked for before publishing. The portable pager adds battery life, touch and statistics, none of which a newcomer at a desk needs to try the pager. Waiting for it would hold a working product back for a phase that the owner's own use, not a newcomer's, should shape.

**Status:** accepted, 2026-10-30 (grilling items 25 and 26). It supersedes ADR 0001's consequence "The portable pager (phase 3) comes before any publishing"; the rest of ADR 0001 stands.

## Consequences

- Phase 5 (the kit's publish, then herdr-pager's v1.0) runs before phase 3. The first push still waits for the desk pager's 7th day in use.
- Both repositories are public from their first push; there is no private stage on GitHub.
- v1.0's README publishes the desk pager's numbers (N1, N2, N3, N7 and N12). N9, N11 and the `stats` summary come with the portable pager, in a later published version (parent 21.4, amended).
- Phase 3 still opens after 7 days in use of the desk pager, as before. Once herdr-pager is public, its later phases change a public product, so each published version needs the same checks v1.0 does.

## Considered options

- **Publish after phase 3, as ADR 0001 planned.** Rejected: the desk pager is already the useful product, and the portable pager's features are for its owner first.
- **Publish privately on GitHub first, then open it.** Rejected by the owner: public from day 1. The export's scans (ADR 0010) and the owner's review before each push do the job a private stage would.
