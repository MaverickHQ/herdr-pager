# The bridge's files

Fixtures of the files in the bridge's directory, `~/.config/herdr-pager/` (parent design §Data models, phase 2 design D2-6, D2-12). The foundation's tests load them, and lanes `cli` and `e2e` can use them, for example `devices list` reading `devices.toml` (task 2.4). Written by hand on 2026-10-22 (phase 2, group 1).

## `devices.toml` (task 1.5)

Two paired pagers, as `provision` writes them: one `[[device]]` table each, with `name`, `key` (the full authorized_keys line, comment included) and `added` (a TOML date-time). `pager-01`'s key is the provisioning fixtures' device key.

| Device | Key type | Fingerprint | Added |
|---|---|---|---|
| `pager-01` | `ssh-ed25519` | `SHA256://a7FlX9Y4t0WSS+W8ANyY4w+5+axSEVbGzNXp6txjY` | 2026-10-22T09:30:00Z |
| `pager-02` | `ssh-ed25519` | `SHA256:gX1c8/ZMiowHYSu8PWSbHJMDfUs8t944xZ5Nx2lT27I` | 2026-10-23T18:05:00Z |

`devices.Load` rejects each file in `devices-invalid/` with an error naming the file and line, `<path>:<line>: <reason>`:

| File | Line | Why |
|---|---|---|
| `syntax.toml` | 7 | An unterminated string |
| `truncated-key.toml` | 8 | `pager-02`'s key is cut short |
| `rsa-key.toml` | 3 | An rsa key: device keys are ssh-ed25519 only (design D4) |
| `duplicate-name.toml` | 7 | Two devices named `pager-01` (revoke goes by name) |
| `no-added.toml` | 1 | No `added`, so the error names the device's `[[device]]` line |
| `unknown-key.toml` | 4 | An unknown key, `fingerprint` |

A missing `devices.toml` is no devices. No key may appear twice: re-provisioning updates the device's entry (parent 16.3).

## `config.toml` (task 1.6)

| Setting | Default | Rule |
|---|---|---|
| `listen` | `0.0.0.0:2277` | `host:port` |
| `herdr_socket` | `$HERDR_SOCKET_PATH`, else `~/.config/herdr/herdr.sock` | An absolute path; a leading `~/` is the home folder |
| `idle_timeout` | `180s` | A Go duration above 0 |
| `log_level` | `info` | `info` or `debug`; `info` never logs card text, detection text or agent names (parent 11.5) |

There's no poll interval: the bridge polls herdr every second (phase 2 R5.3), so `poll_interval` is an unknown setting. An empty or missing value takes its default. A missing `config.toml` is an error (`init` writes it).

- `config-empty.toml` loads as every default.
- `config.toml` sets every value away from its default, and wins over `$HERDR_SOCKET_PATH`.
- `config.Load` rejects each file in `config-invalid/` with `<path>:<line>: <setting>: <reason>`:

| File | Line | Why |
|---|---|---|
| `poll-interval.toml` | 2 | `poll_interval` isn't a setting |
| `bad-listen.toml` | 2 | `2277` isn't `host:port` |
| `bad-idle-timeout.toml` | 3 | `three minutes` isn't a duration |
| `relative-socket.toml` | 1 | `herdr.sock` isn't an absolute path |
| `bad-log-level.toml` | 2 | `trace` isn't a log level |

## `status.json` (task 1.7)

What `serve` tells the CLI (design D2-12): one JSON object, rewritten atomically (a temporary file renamed over it, mode 0600) every second and on change.

| Key | What |
|---|---|
| `written` | When serve wrote it (RFC 3339, UTC) |
| `listening` | The address serve listens on |
| `herdr` | herdr's state, `ok` or `offline` |
| `socket` | herdr's socket path |
| `devices` | The paired devices' names (`[]` for none, never `null`) |
| `connected` | The names with a session open |

A bridge is running only if the file exists and `written` is at most 5 s from now; otherwise `status` and `devices list` say none is running, and no device shows connected. The fixture was written at 2026-10-23T18:10:00Z, listening on `0.0.0.0:2277`, with herdr up, both fixture devices paired and `pager-01` connected; a test passes its own `now`, so the fixture is fresh at 18:10:05 and stale after.
