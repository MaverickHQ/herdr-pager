# The threat model

What herdr-pager defends against, how, and what it leaves to you. This is the summary; the full model, with each decision behind it, is in the design (`docs/design/design.md` §Threat model). The numbers (T1, T2 …) are the design's, so each row can be found there. To report a problem, see [`SECURITY.md`](../SECURITY.md).

## First: what it isn't

**The pager is an attention device, not a security gate.** Any process in a herdr pane can already drive herdr's socket (`~/.config/herdr/herdr.sock`) and answer its own prompts, pager or no pager. A prompt-injected agent doesn't need the pager to approve itself (T4). herdr-pager makes the approvals you give from a button as safe as the ones you'd give at the keyboard; it adds no gate the agent can't walk around. A real gate belongs in the agent's own permission settings.

## What's trusted

| Input | From | Trusted? |
|---|---|---|
| What a pager sends | Over SSH, from a paired pager | Its key is checked, but what it says isn't trusted: it may send only verbs from a fixed list, and the bridge decides which keys a verb means |
| What herdr reports about agents | Terminal text that agents wrote | Never trusted: it's cleaned before it reaches the screen, and never passed to a program |
| Pairing over USB | Someone with the pager in hand | Only after you confirm on the pager, with BOOT |
| `config.toml`, `keymaps.toml`, `risk.toml`, `devices.toml` | You, the Mac's user | Trusted |
| The setup page's requests | The browser on this Mac | Only with the page's one-time token, from its own origin |

## The threats

| # | Threat | What stops it | What's left |
|---|---|---|---|
| T1 | Someone on the network connects to the bridge's port, 2277 | Only the full public keys of paired pagers get in; no password or keyboard-interactive login; at most 3 tries; Go's maintained `x/crypto/ssh` | A bug in `x/crypto/ssh` before authentication. Keep the bridge updated |
| T2 | A man in the middle of the pager's link | The pager pins the bridge's whole host key when it's paired over USB, read from the bridge's file, so there's no trust on first use. On a different key it stops before authenticating, shows HOST KEY CHANGED, and tries again only every 5 minutes | Whoever can answer the pager's dial on your network can keep it on HOST KEY CHANGED, but never learns its key or a verdict |
| T3 | A lost or stolen pager | It can only approve, deny, interrupt or jump, only on the prompts it's shown, and only from a network that reaches the bridge. `pager-bridge devices revoke` takes effect within 2 s. A `devices.toml` that won't load lets no pager in, so a broken edit can't keep a revoked one paired | **Accepted:** the pager's storage isn't encrypted, so whoever holds it, or any computer it's plugged into over USB, can read its key and Wi-Fi passwords. Revoke it and change those passwords (README §Safety). Encrypting its flash is planned for a later version |
| T4 | A prompt-injected agent approves its own prompt | Out of scope: the agent can already drive herdr's socket without the pager | **Accepted.** A real gate belongs in the agent's permission settings |
| T5 | Terminal text disguises what's being approved (bidi characters, escape sequences, look-alike letters) | The bridge cleans every card to printable ASCII, removing control, bidi and zero-width characters, and the pager filters again. A command, path, URL or MCP call holding an escape code or invisible character fails closed: deny only | An agent can still write misleading plain text, as it can at the keyboard |
| T6 | A verdict lands on a different prompt (a race) | The bridge reads the agent again just before it sends any key, and checks the card's `state_change_seq` and a hash of its text; one verdict per pane at a time; nothing is queued | A window of milliseconds between that read and the keys. **Accepted** until herdr offers an approve call bound to the request |
| T7 | A question taken for a permission | The keymap's `permission_match`; a question card offers no verdict | An agent's new version can change its prompts. The captures and their tests catch it (CONTRIBUTING.md §The keymap guide) |
| T8 | A pager sends keys or text of its own | A fixed list of verbs; keys come only from the bridge's keymap; a fixed list of herdr methods | None |
| T9 | Command injection through agent text | No program the bridge runs takes agent- or device-derived text as an argument; herdr is reached only through its JSON socket | None |
| T10 | Someone pairs a pager behind your back | USB only, so it needs the pager in hand; both full fingerprints shown on the pager and the Mac; a BOOT press on the pager, then your confirmation on the page or the terminal, before any Wi-Fi password is sent (so a device that isn't your pager, answering on the port, gets none) | Someone with both the pager and your Mac in hand |
| T11 | A flood against the bridge | A session's lines are capped at 512 bytes and 10 messages a second, five bad messages close it, and it closes when idle. Before authentication: 15 s from connecting to a session; at most 32 connections in their handshake, 4 from one address, and a full set makes way for an address holding fewer, so a pager (one connection) gets past a few busy addresses; running out of file descriptors isn't fatal; their log lines are limited to one a second | 32 or more addresses each holding a connection can keep a pager out (it keeps retrying), and one host on your network can claim many addresses. If that matters, set `listen` in `config.toml` to the Mac's address on that network only, or firewall the port |
| T12 | A weak device key from an unseeded random number generator | The pager makes its key only with its radio on, and reseeds libssh's generator first; a test checks the order, in the emulator and on the board | None. (A key made by firmware from before the published versions may be weak: factory reset it and pair again) |
| T13 | The supply chain | Few dependencies, each checked with valvur before it's added; Go's `go.sum`; exact versions in the firmware's component manifests (libssh `==0.12.2`); lcd349-kit from its public repository at a tag; the committed component lock files pin every component's version and hash | A published tag or a registry's component is trusted as its authors publish it |
| T14 | Presses in a pocket or a bag | The press that wakes the screen does nothing else; touch never wakes the screen; a card must have been on screen 500 ms; approve is a 1 s hold (2 s on a danger card) | A pocket can still deny, which fails safe |
| T16 | A danger rule is missed or dodged (an obfuscated command, a new tool) | The rules are a reading aid, and the docs say so. The card always shows what's asked first. A card whose command, path, URL or MCP call can't be read in full, or whose approve option can't be confirmed as a plain "Yes", fails closed: deny only, with the rules run over the whole prompt box | **Accepted:** a card with no banner isn't a safe card. A card that failed closed is answered at the keyboard |
| T17 | The usage statistics leak activity patterns | Not in v1.0: the statistics come later, and will hold no text, names or keys, only keyed hashes of pane ids, in a file only you can read | Timing patterns on the Mac's own disk |
| T18 | A web page or a local process drives the setup page (CSRF, DNS rebinding, a log line carrying script) to pair or revoke a pager, or to read a Wi-Fi password | It listens on 127.0.0.1 only, on a random port, behind a one-time token on every request; it checks the Host and Origin headers; it sends a strict content security policy and shows log lines only as text; it runs only while `pager-bridge setup` runs, and stops 5 minutes after its last tab closes. A password is never stored, logged or shown; pairing still takes BOOT on the pager; the page takes no verdicts | A process already running as you, which could read `devices.toml` and drive herdr's socket anyway |
| T19 | A tampered published version (a compromised build, tag or tap) | CI builds each version from its tag, never on a maintainer's Mac, with build attestations and `SHA256SUMS`; the Homebrew formula pins the archive's sha256; every CI action is pinned by its commit SHA; tags and `main` are protected; the maintainer approves every push | **Accepted:** there's no Apple Developer ID, so the bridge is unsigned and macOS can't vouch for it; the checksums and attestations stand in (`gh attestation verify`). A compromised GitHub account defeats all of this |

## The accepted risks, in one place

- herdr-pager is not a security gate (T4).
- The pager's storage isn't encrypted (T3): plug it only into computers you trust.
- A few milliseconds between the bridge's last read and its keys (T6).
- The danger rules miss what they don't know (T16).
- 32 or more addresses can keep a pager out (T11).
- The bridge isn't signed (T19).
