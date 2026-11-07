# Publish from a fresh history

The public repositories (herdr-pager and lcd349-kit) start from fresh histories: each published version is one commit, made by an export that copies an allowlist of the private working copy into a clone of the public repository. The private repositories stay the working copies, with their specs, run logs and history. Their histories hold personal data in hundreds of commits (Wi-Fi names, home-folder paths, an email address, real key fingerprints, spend figures), and rewriting them would never prove that nothing was missed.

**Status:** accepted, 2026-10-30 (grilling items 25 and 26).

## Consequences

- Each export scans what it is about to commit: for a private list of the owner's values, kept outside every repository; with gitleaks; and with valvur. The owner reads the diff before any push.
- The public history is coarse: one commit per published version, with a written message, not the working history. The working history and its run logs stay private.
- `CONTRIBUTING.md` asks for issues, not pull requests, for now: a pull request would land in the public repository, which the private one doesn't follow.
- Code comments may cite spec items that stay private (open items, phase task numbers). The parent requirements and design, and phase 2's, are public under `docs/design/`, so most citations resolve.
- Never `git push --mirror` from a working copy: it would publish every branch and stash.

## Considered options

- **Push the working repository, history and all.** Rejected: it publishes everything in the history.
- **Rewrite the history (filter-repo), then push.** Rejected: one missed value is public for good, and the run logs would still need removing.
- **Make the public repository the working copy from v1.0 on.** Rejected for now: the specs and run logs, which the phases need, would have to move somewhere private, and every commit would need the same review as an export.
