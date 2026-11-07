# pager-bridge: the guide

`pager-bridge` is the half of herdr-pager that runs on the Mac (`bridge/`, Go, one binary). It serves paired pagers over SSH, and it's the only thing that talks to herdr. The README's quick start covers the first run; this guide covers every command, the files, the settings and running it at login.

What it does, and what it never does:
- It polls herdr's socket once a second (`agent.list`, then `tab.list` and `workspace.list` for each agent's name), and sends each connected pager a snapshot whenever an agent changes. It sends terminal text only as a card, when the pager opens one, sanitized to printable ASCII.
- It calls only `agent.list`, `agent.get`, `agent.read`, `agent.send_keys`, `agent.focus`, and the read-only `tab.list` and `workspace.list`. It sends only the keys in its keymap: a pager can't send text or keys of its own.
- Before it sends an approval's keys, it reads the agent again. If the prompt is no longer the one the pager showed, it sends nothing and the pager shows SCREEN CHANGED.
- It talks to herdr only through the JSON socket, never by running a program with text from an agent or a pager.
- It needs no `sshd`, Remote Login or root: its SSH server is its own, on port 2277.

The pager is an attention device, not a security gate: any process in a herdr pane can already drive herdr's socket (README §Safety).

## Build it

```sh
cd bridge && go build -o ../bin/pager-bridge ./cmd/pager-bridge
```

Go 1.26. The commands below are run from the repository's root as `bin/pager-bridge`.

**Its version.** `pager-bridge --version` prints it. A published version says its number (`1.0.0`); any other build says the number it's building toward with `-dev` (`1.0.0-dev`), and so does firmware built from the same tree. Both come from `bridge/internal/version/VERSION`.

**The macOS firewall.** If it's on, allow the binary, or the pagers' connections never arrive:

```sh
sudo /usr/libexec/ApplicationFirewall/socketfilterfw --add "$PWD/bin/pager-bridge"
sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp "$PWD/bin/pager-bridge"
```

Each build is a new binary, so allow it again after each `go build`.

**Update the pager and the bridge together.** A bridge sends a card of 4 lines (an MCP call's tool and 3 arguments) only to a pager whose hello says it draws 4; to a pager from before that, it sends such a call as TOOL NOT READ, deny only. A newer pager works with an older bridge. Flash the firmware from the same commit as the bridge.

## Its files

Everything lives in `~/.config/herdr-pager/` (mode 0700). Nothing else on the Mac is changed.

| File | Mode | What |
|---|---|---|
| `host_ed25519` | 0600 | The bridge's SSH host key. Every paired pager pins it; replacing it means pairing every pager again. |
| `host_ed25519.pub` | 0644 | Its public half. |
| `config.toml` | 0600 | The settings (below). |
| `devices.toml` | 0600 | The paired pagers: each one's name, full public key and pairing date. Written atomically by `provision` and `devices revoke`. |
| `status.json` | 0600 | Written by a running `serve` every second, for `status` and `devices list`. Missing, or older than 5 s, means no bridge is running. |
| `keymaps.toml` | | Optional: your keymap overrides (§Keymaps and danger rules). |
| `risk.toml` | | Optional: your danger rules (§Keymaps and danger rules). |

`init` creates the first four. Back up `host_ed25519` if you'd rather not re-pair after reinstalling.

## Settings: `config.toml`

`init` writes every setting at its default. An empty or missing value takes its default; an unknown setting or a bad value stops `serve` with an error naming the file and line.

| Setting | Default | What |
|---|---|---|
| `listen` | `"0.0.0.0:2277"` | The address the SSH server listens on, as `host:port`. `provision` gives pagers this port. |
| `herdr_socket` | `""` | herdr's socket. Empty: `$HERDR_SOCKET_PATH`, else `~/.config/herdr/herdr.sock`. A path must be absolute or start with `~/`. |
| `idle_timeout` | `"180s"` | A pager's session closes after this long without a message. Pagers ping every 60 s. |
| `log_level` | `"info"` | `info` or `debug`. |

There's no poll interval: the bridge polls herdr every second. Under launchd the bridge doesn't see your shell's variables, so if herdr's socket isn't at the default, set `herdr_socket` here rather than relying on `$HERDR_SOCKET_PATH`.

## Commands

Every command runs without prompts, given its flags and stdin. Each exits 0 on success, 1 on a failure (saying why on stderr) and 2 on a usage error. `bin/pager-bridge help` lists them.

### `init`

Creates the directory and its files: the ed25519 host key, `config.toml` and an empty `devices.toml`. It prints the host key's full SHA256 fingerprint and the listen address. Run again, it changes nothing and prints the same fingerprint. If herdr isn't reachable at the socket it would use, it still succeeds and warns, naming the path.

### `serve`

Serves paired pagers until SIGINT or SIGTERM, logging to stderr.

- Before it listens, it loads `config.toml`, the danger rules, the keymaps, the host key and `devices.toml`. Any error there stops it, naming the file.
- It polls herdr every second. While herdr is unreachable, pagers show HERDR OFFLINE; snapshots resume within 2 s of herdr coming back.
- It reads `devices.toml` again within 500 ms of a change: a newly paired pager can connect at once, and a revoked one's session closes within 2 s. If a hand edit leaves the file unloadable, no pager can connect until it loads again, and `status` says why.
- It keeps `status.json` current, and removes it when it stops.
- On SIGINT or SIGTERM it closes every session and exits 0 within 2 s.

A pager's session is held to what a pager needs: its messages are limited to `hello`, `ping`, `list`, `show`, `approve`, `deny`, `interrupt` and `jump`. A line over 512 bytes, or five invalid messages, closes the session; more than 10 messages a second are dropped. When the same pager connects again, its older session closes.

Connections that haven't authenticated are held to little too: at most 32 at once in their SSH handshake, and at most 4 from one address; one more is closed as soon as it's accepted, unless all 32 are taken and some address holds more than one more than the newcomer's, whose oldest connection then makes way. So a few busy addresses can't keep a pager out; 32 or more can, which is why the port is best kept to your LAN. Each has 15 s to authenticate and start its session. If the Mac runs out of file descriptors, `serve` waits and accepts again rather than exiting.

### `status [--json]`

Shows whether a bridge is running and where it listens, herdr's state (`ok` or `offline`; `unknown` while no bridge runs), herdr's socket, the paired pagers and which are connected. `--json` prints the same as one JSON object:

```json
{"running":true,"listening":"0.0.0.0:2277","herdr":"ok","socket":"/Users/<you>/.config/herdr/herdr.sock","devices":["pager-01"],"connected":["pager-01"]}
```

### `devices list`

Lists the paired pagers: name, key type, full SHA256 fingerprint, the date it was paired, and whether it's connected now (which only a running bridge can say).

### `devices revoke <name>`

Removes the pager from `devices.toml`. A running bridge closes its session within 2 s and refuses it from then on; the pager shows NOT AUTHORISED. For a lost pager, then change the passwords of the Wi-Fi networks it knew: its storage isn't encrypted.

### `provision`: pair a pager

```
bin/pager-bridge provision --port <serial> --name <name> --wifi-ssid <ssid> [--wifi-ssid <ssid>]... --wifi-pass-stdin [--host <host>] [--yes]
```

| Flag | What |
|---|---|
| `--port` | The pager's USB serial port: `ls /dev/cu.usbmodem*`. |
| `--name` | The pager's name, 1 to 24 of `A-Z a-z 0-9 . _ -`, such as `pager-01`. |
| `--wifi-ssid` | A Wi-Fi network, repeated for up to 4, highest priority first. 2.4 GHz only: the ESP32-S3 can't join 5 GHz. The name must match exactly, apostrophes included: an iPhone's hotspot uses a curly one (`Sam’s iPhone`). If this Mac has saved a network whose name differs only in its apostrophes, quotes, dashes, case or spaces, `provision` warns before it opens the port and gives the Mac's spelling. It reads the Mac's list with `networksetup -listpreferredwirelessnetworks`. |
| `--wifi-pass-stdin` | Required. Each network's password is one line of stdin, in `--wifi-ssid` order; an empty line is an open network. |
| `--host` | The address the pager dials. Default: this Mac's `<LocalHostName>.local` (from `scutil --get LocalHostName`, else the host name). An IP address always works, but may change. |
| `--yes` | Don't ask on the terminal whether the fingerprints match: for the board checks and the emulator tests, which run unattended. BOOT on the pager is still needed. Without it, `provision` needs a terminal (`/dev/tty`) to ask on, since its stdin carries the passwords. |

A password is never an argument, and `provision` never prints or logs one. To type them unseen:

```sh
read -rs pass1 && read -rs pass2 && printf '%s\n%s\n' "$pass1" "$pass2" | bin/pager-bridge provision \
    --port /dev/cu.usbmodemXXXX --name pager-01 --wifi-ssid "Home" --wifi-ssid "Office" --wifi-pass-stdin
unset pass1 pass2
```

What happens:
1. `provision` opens the port, which resets the board. A pager that has never been paired, or has been factory reset, starts in pairing mode (PAIR VIA USB). Any other must be put in pairing mode now, with **Pair again** in its global menu; it keeps its key. `provision` waits up to 120 s.
2. It reads the pager's public key, which must be ed25519; any other type is refused. It sends the pager an offer with nothing secret: its name, the host and port, the bridge's full host key and this Mac's name.
3. Both ends show the same two full fingerprints, the pager's key and the bridge's. Check them.
4. Press BOOT on the pager within 30 s; PWR cancels. The pager then shows NOW CONFIRM ON *your Mac* and waits 30 s.
5. `provision` asks `Do these match the pager's screen? [y/N]` on the terminal. Only on `y` does it send the Wi-Fi networks and their passwords, which the pager takes without another press. Only when the pager reports it has saved them does `provision` add it to `devices.toml`, and say `Paired <name>.`
6. The pager joins the highest-priority saved network it can see and dials the bridge.

If anything fails (no hello, PWR, the 30 s passing, a refused key, an answer other than `y`), `provision` exits 1 and `devices.toml` is unchanged. A pager whose firmware is older than the bridge refuses the offer: flash the firmware shipped with the bridge first (`pager-bridge setup`'s Flash step). The pager and the bridge are updated together. Pairing the same pager again, with the same key, updates its entry rather than adding another. A name already paired with a different key is refused: revoke it first, or choose another name.

### `stdio`

Runs one pager session on stdin and stdout, exactly as over SSH, with no authentication: whoever runs it is trusted. It's for tests, and for later use as an OpenSSH forced command. It polls herdr as `serve` does, and exits 0 when stdin ends, the session goes idle, or on SIGINT or SIGTERM.

## Run it at login

`contrib/launchd/dev.herdr-pager.bridge.plist` is a launchd user agent that runs `pager-bridge serve` at login and restarts it if it fails. It names the binary and the log by placeholders, `__REPO__` (this repository's absolute path) and `__HOME__`, filled in as it's installed. From the repository's root, after building:

```sh
mkdir -p ~/Library/LaunchAgents ~/Library/Logs/herdr-pager
sed -e "s|__REPO__|$PWD|g" -e "s|__HOME__|$HOME|g" contrib/launchd/dev.herdr-pager.bridge.plist \
    > ~/Library/LaunchAgents/dev.herdr-pager.bridge.plist
plutil -lint ~/Library/LaunchAgents/dev.herdr-pager.bridge.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.herdr-pager.bridge.plist
```

| To | Run |
|---|---|
| Check it's running | `bin/pager-bridge status`, or `launchctl print gui/$(id -u)/dev.herdr-pager.bridge` |
| Read its log | `tail -f ~/Library/Logs/herdr-pager/bridge.log` |
| Restart it, after a rebuild | `launchctl kickstart -k gui/$(id -u)/dev.herdr-pager.bridge` (allow the new binary through the firewall first) |
| Stop it until the next login | `launchctl bootout gui/$(id -u)/dev.herdr-pager.bridge` |
| Remove it | `launchctl bootout gui/$(id -u)/dev.herdr-pager.bridge`, then `rm ~/Library/LaunchAgents/dev.herdr-pager.bridge.plist` |

Run only one bridge at a time: a second `serve` can't listen on the same port.

## Logs

`serve` logs to stderr (under launchd, to `~/Library/Logs/herdr-pager/bridge.log`), one `key=value` line per event. At `info`, the default, the log carries pane ids, verbs, results, timings and pager names, and **never card text, detection text or agent names**. A refused key is logged with its full SHA256 fingerprint and the address it came from, and nothing else the client sent. Invalid messages are logged by type only.

What a client that hasn't authenticated can cause is logged at most once a second for each kind of line: a refused key, a connection with no session in 15 s, a connection closed because too many are in their handshake. The next line of that kind says how many were held back (`held_back=N`). A session's dropped messages are logged the same way. So a flood of connections can't flood the log.

**Rotating the log.** launchd opens the log once, when it starts `serve`, and hands `serve` that open file as its stderr: `serve` never opens the log itself, and has no signal that makes it open the file again. So **after a rotation, `serve` must be restarted**, or it goes on writing to the file the rotation moved aside. macOS's `newsyslog` can do the moving. As root, create `/etc/newsyslog.d/herdr-pager.conf` with one line, giving your home's absolute path (newsyslog expands no `~`) and your user name:

```
# logfile                                        owner:group  mode  count  size(KB)  when  flags
/Users/<you>/Library/Logs/herdr-pager/bridge.log   you:staff    600   5      1024      *     N
```

newsyslog checks it every half hour: once the log passes 1 MB, it moves it to `bridge.log.0` (keeping 5) and starts an empty `bridge.log`. `N` means there's no process to signal, and the moved file is left uncompressed, since `serve` is still writing to it. Then restart `serve`, which starts writing the new `bridge.log`: `launchctl kickstart -k gui/$(id -u)/dev.herdr-pager.bridge`. Until that restart, nothing is lost and newsyslog moves nothing more (the new `bridge.log` stays empty), so the one file `serve` writes keeps growing. `sudo newsyslog -nvv` shows what it would do. A logout and login restarts `serve` too.

## Keymaps and danger rules

- **Keymaps** say which keys each verb sends to each kind of agent, and how to read its prompts. The defaults are embedded: `bridge/keymaps/claude.toml` for Claude Code. An agent whose kind has no keymap offers only jump. `~/.config/herdr-pager/keymaps.toml`, if it's there, overrides them, in the same format (parent design §Keymaps). Keys are limited to `esc`, `enter`, `tab`, the arrows, `backspace`, `0`–`9`, `a`–`z` and `ctrl+<letter>`. The prompt box's title decides which pattern reads the card's first line: `file_match` (the path: an edit, a new file, or a read outside the folder) when `file_prompt` matches, else `url_match` (the URL of a web fetch) when `url_prompt` matches, else `mcp_match` (an MCP tool call: its server and tool, and its arguments) when `mcp_prompt` matches, else `command_match` (the command). `url_match` reads the host the prompt names too, and the bridge takes the URL only if it's an http or https URL of that host with no user name, and the `url:` and `prompt:` lines are the only ones that start so; the card shows the host, bare and whole, on its second line, and fails closed on a host longer than a line. An MCP card shows the server and tool first, whole (it fails closed on one longer than a line), then each argument on a line of its own, its name whole and a long value cut in the middle; a call with no arguments, more than 3 (the card holds 4 lines), an argument whose value Claude Code draws over several lines, or a name that leaves its value fewer than 10 characters reads none, TOOL NOT READ. The danger rules read each value from inside its opening quote. An MCP card names the tool as its server describes it, so a server you don't trust could name a tool misleadingly: only install MCP servers you trust. A permission card offers approve only when that pattern read it, it holds no escape code, control character (but newline and tab) or invisible character, and the `approve_option` pattern confirms that the option approve's keys pick is the approve, with exactly one line of the box asking the question (`question_match`); otherwise the card says COMMAND NOT READ (FILE NOT READ on a file prompt, URL NOT READ on a fetch prompt, TOOL NOT READ on an MCP call) and offers only deny. A keymap with no `approve_option` never offers approve, and with no `file_prompt`, `url_prompt` or `mcp_prompt` treats every prompt as a command. Claude Code's keymap reads Bash commands, file edits and new files, reads outside the folder, web fetches and MCP tool calls (`bridge/testdata/herdr/claude/DETECTION.md`); any other prompt can't be approved from the pager.
- **Danger rules** flag risky commands, file paths and URLs on a card (README §Safety), read with their quote characters (`"` and `'`) and their `\n`, `\t`, `\"` and `\'` escapes as spaces, so `bash -c "sudo …"` and an MCP argument's quoted value are flagged too (O-90's review): among them, since O-88, reading or writing a file that holds a secret (SECRETS) and fetching a local address (LOCAL ADDRESS). The defaults are embedded (`bridge/internal/risk/risk.toml`); `~/.config/herdr-pager/risk.toml`, if it's there, holds your own rules, read with the embedded ones and in their format (parent design §Danger rules). Every rule carries its own `tests_match` and `tests_no_match` examples.

An invalid override, or a rule whose own examples fail, stops `serve` with an error naming the file and the line or key. Bad settings never fall back silently.

## When the pager says

| The pager shows | What it means | What to do |
|---|---|---|
| PAIR VIA USB | It has never been paired, or was factory reset | Pair it (`provision`) |
| WRONG WIFI PASSWORD | A saved network refused its password three times in a row | Pair it again with the right password (the setup page's Change the Wi-Fi networks) |
| NO WIFI | None of its saved networks is in range; it scans every 60 s | Check the network is 2.4 GHz and on, or pair again with another |
| CONNECTING, then NO BRIDGE ON THIS NETWORK (or, over a list kept from before, the link isn't up in the status bar) | It's on Wi-Fi but can't reach the bridge: on a phone's hotspot, the Mac must be on it too | `bin/pager-bridge status`; check the firewall allows the binary, the Mac is on the same network, and `.local` names resolve there (else pair again with `--host <ip>`). PWR shows device info: the address it dials, and why it last started (POWER ON, RESTART, CRASH, BROWNOUT or RESET) |
| NOT AUTHORISED | The bridge refused its key: it was revoked, or `devices.toml` lost it. It retries every 5 minutes | Pair it again |
| HOST KEY CHANGED - RE-PAIR VIA USB | The bridge answering isn't the one it paired with: `host_ed25519` was replaced, or another machine answered. It never authenticates to that key, and tries again every 5 minutes; if the bridge it paired with answers, it connects | If you replaced the key, pair it again; if not, find out what answered |
| HERDR OFFLINE | The bridge can't reach herdr's socket | Start herdr; check `herdr_socket` and `bin/pager-bridge status` |
