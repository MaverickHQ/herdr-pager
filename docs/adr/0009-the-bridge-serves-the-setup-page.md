# The bridge serves the setup page

A newcomer sets herdr-pager up from one page in the browser, served by the bridge on their own Mac while `pager-bridge setup` runs. The page flashes the pager over USB, takes the Wi-Fi networks, pairs (still confirmed with BOOT on the pager), checks the firewall, turns on run at login and waits for the first page, showing the live output of each step. Afterwards the same page manages the setup: devices, versions and updates, and the bridge's checks. The plan before this was a static flasher page on GitHub (phase 5 design D5P-1), which could only flash: the Wi-Fi, the pairing and everything else stayed in the terminal. The owner asked for the simplest start for a newcomer, all in one place, with the terminal's output live in the browser.

**Status:** accepted, 2026-10-30 (grilling items 25 and 26). It replaces phase 5's D5P-1.

## Consequences

- The bridge gains a local web server, the first listener besides its SSH port. It listens only on 127.0.0.1, on a random port, behind a one-time token, and checks the Host and Origin headers; it runs only while `pager-bridge setup` runs, and stops 5 minutes after its last tab closes. The threat model gains an entry for it.
- The page never approves, denies or sends keys to an agent, and never shows cards: verdicts stay on the pager. Log lines can carry agents' names, so the page shows them only as text, under a strict content security policy.
- Wi-Fi passwords travel from the page to the bridge and over USB to the pager, in memory only; they are never written, logged or shown. Pairing still needs the owner's BOOT press on the pager (parent 22.3, amended: the bridge pairs, at the page's request).
- Flashing uses ESP Web Tools over WebSerial, vendored into the bridge, so it needs Chrome or Edge. A first install erases the pager; an update keeps its pairing.
- The firmware image ships with the bridge (in its Homebrew formula and its archive), so the page always flashes the firmware that matches the bridge. There is no hosted flasher page (parent 22.1, amended).
- A new `pager-bridge doctor` holds every check of the Mac's setup, so the page, the Claude Code skill and the terminal give the same answers.

## Considered options

- **A static flasher page on GitHub (D5P-1).** Rejected: it flashes, but a newcomer still has to pair and configure in the terminal, and it can't show the bridge's output.
- **The bridge flashes in Go, any browser.** Rejected: the ESP32-S3's download protocol would be new code to own, where ESP Web Tools is proven.
- **A Claude Code skill and MCP server as the setup.** Not as the setup: not every newcomer uses Claude Code, and a Wi-Fi password must never pass through a model. A plugin with a setup skill ships beside the page; it runs the CLI and opens the page. An MCP server would give agents, which the pager supervises, tools to revoke it; it isn't in v1.0.
