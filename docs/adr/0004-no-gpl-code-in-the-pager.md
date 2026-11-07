# No GPL code in herdr-pager

herdr-pager's firmware and bridge contain no GPL-licensed code. An LGPL library is acceptable only as a separately identifiable library, with the project's full source and build files published alongside any binaries. The pager's default licence for publishing is Apache-2.0 (ADR 0001). Distributing firmware that links GPL code, for example through the web flasher, would force the whole firmware under the GPL, while LGPL libssh is compatible once the source is available for relinking.

**Status:** accepted, 2026-10-18 (P-01 grilling).

## Consequences

- wolfSSH (GPLv3 or commercial) is no longer the SSH fallback; the registry version also carried CVE-2026-16516 and has no ESP-IDF client example.
- The SSH spike tries, in order:
  1. `david-cermak/libssh` (LGPL-2.1, a plain ESP-IDF component);
  2. LibSSH-ESP32 (LGPL-2.1, needs the Arduino core);
  3. libssh2 via `skuodi/libssh2_esp` (BSD-3, ECDSA keys only, and only at a commit carrying the 2026 security fixes).
- Every new dependency's licence is checked against this rule as well as with valvur.
