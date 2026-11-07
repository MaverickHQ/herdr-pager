# The doctor's checks

`pager-bridge doctor --json` runs these checks in this order. Each failed one carries its own `detail`, `fix` and `commands`. Those are the authority; this page adds what a failure means for the pager, and who carries out the fix:

- **you:** with the person's yes, as SKILL.md §What you may run allows;
- **the page:** the setup page, `pager-bridge setup`;
- **the person:** in their own terminal or System Settings.

A state that isn't listed here: go by the check's `detail` and `fix`, and say you're doing so.

## `herdr`: herdr's socket

The bridge reads the agents from herdr's socket, `~/.config/herdr/herdr.sock` unless `herdr_socket` in `~/.config/herdr-pager/config.toml` or `$HERDR_SOCKET_PATH` says otherwise. Without herdr, the pager shows HERDR OFFLINE and pages nothing.

| State | Means | Fix | Who |
|---|---|---|---|
| `ok` | herdr answers | | |
| `missing` | No socket there: herdr isn't running, or its socket is elsewhere | Start herdr. If its socket is elsewhere, set `herdr_socket` to its path | the person |
| `refused` | The socket is there, but nothing answers: herdr has stopped | Start herdr | the person |
| `not-socket` | The path set is a file, not a socket | Set `herdr_socket` to herdr's socket | the person |
| `error` | The doctor couldn't look, usually a permission problem | As its `detail` says | the person |

Starting herdr is the person's: it runs their agents.

## `keys`: the bridge's keys

The bridge's identity is its SSH host key, `~/.config/herdr-pager/host_ed25519`. Each paired pager pinned its public half. `config.toml` sits beside it.

| State | Means | Fix | Who |
|---|---|---|---|
| `ok` | Both load. `detail` gives the host key's fingerprint | | |
| `missing` | No host key, or no `config.toml` | `pager-bridge init` makes what's missing, and keeps a host key that's there. The setup page does it too | you, or the page |
| `invalid` | The host key, or a line of `config.toml`, won't load | For `config.toml`: fix the line `detail` names (`docs/bridge.md` lists the settings). For the host key: see below | the person |

Never move, replace or delete a host key yourself. A new key means pairing every pager again: until then each shows HOST KEY CHANGED - RE-PAIR VIA USB. Say that, and leave the decision to the person.

## `port`: port 2277

The pagers dial the bridge on this port (`listen` in `config.toml`; `0.0.0.0:2277` by default).

| State | Means | Fix | Who |
|---|---|---|---|
| `bridge` | A running bridge holds it | | |
| `free` | Free, and no bridge is running: see the `login` and `devices` checks | | |
| `taken` | Another process listens on it, so the bridge can't. Often a second `pager-bridge serve`, left running in a terminal | The `lsof` command in `commands` names the process. Stop it; one bridge serves every pager | you run `lsof` (a read); the person stops the process |
| `unknown` | The doctor couldn't tell | Run the `lsof` command | you |

Don't kill a process yourself: say what `lsof` found, and let the person stop it.

## `firewall`: the macOS firewall

If the firewall is on and doesn't allow the bridge, the pager's calls never arrive. The pager then shows CONNECTING, then NO BRIDGE ON THIS NETWORK, although the Mac is on the same network.

| State | Means | Fix | Who |
|---|---|---|---|
| `off` | Off: the calls get through | | |
| `allowing` | On, and it allows the bridge's own path | | |
| `blocking` | On, and it blocks the bridge, or doesn't list it | The two `sudo socketfilterfw` commands in `commands`, or the setup page's firewall step | the person, or the page |
| `block-all` | On, with Block all incoming connections | System Settings → Network → Firewall → Options…: turn off Block all incoming connections. Then run the doctor again | the person |
| `unknown` | The doctor couldn't read it | Look in System Settings → Network → Firewall | the person |

The firewall knows the bridge by its path. A Homebrew upgrade or a rebuild makes a new binary, which needs allowing again: a firewall that passed last week can block today.

## `login`: run at login

Something must start `pager-bridge serve` at login. That's Homebrew's service for a Homebrew install, or the launchd agent `dev.herdr-pager.bridge` for a source build.

| State | Means | Fix | Who |
|---|---|---|---|
| `brew` | `brew services` runs it | | |
| `launchd` | The launchd agent runs this bridge (its path, or a symlink to it) | | |
| `other` | The launchd agent runs another copy of the bridge (`detail` names it), or the doctor can't read which | Run at login on the setup page offers to switch the agent to this bridge. It rewrites the agent and gives the two `launchctl` commands that reload it | the person |
| `off` | Installed with Homebrew, but its service isn't started | `brew services start herdr-pager`, or Run at login on the setup page | you, or the page |
| `error` | Homebrew's service started it, and it failed | Read the tail of the bridge's log for why, and fix that. Then `brew services restart herdr-pager` | you read the log; the fix depends on it |
| `both` | Both run it: two bridges for one port | Keep Homebrew's. Remove the launchd agent with the commands in `commands` | the person |
| `none` | Nothing runs it at login | Homebrew: `brew services start herdr-pager`. Source build: the launchd agent, as `docs/bridge.md` §Run it at login says | you (Homebrew), or the person (launchd) |
| `unknown` | The doctor couldn't ask Homebrew | As `fix` says | the person |

The bridge's log says why it failed. A common cause is a `config.toml`, `keymaps.toml` or `risk.toml` it won't load: `serve` stops with an error naming the file and the line.

## `devices`: the paired pagers

The pagers in `devices.toml`, and whether a running bridge has each one connected. `devices` lists each by `name` and full `fingerprint`. The fingerprint is the label the pager shows as DEV in its device info.

| State | Means | Fix | Who |
|---|---|---|---|
| `connected` | Every paired pager is connected | | |
| `none` | No pager is paired | Pair one on the setup page | the page |
| `not-running` | Paired, but no bridge is running, so none can connect | Fix the `login` check first. Its fix starts the bridge | as `login` says |
| `disconnected` | The bridge runs, but these pagers aren't connected | Ask what each pager's screen says, and use `references/screens.md`. If `firewall` fails, fix that first | the person looks; you explain |
| `error` | `devices.toml` won't load | Tell the person the line `detail` names. They fix it, or pair the pager again on the setup page. Don't edit the file yourself | the person, or the page |

A pager that's disconnected says why on its own screen, and the Mac can't see that. The usual causes:

- the pager is on a different network from the Mac (a phone's hotspot the Mac isn't on);
- the network is 5 GHz only;
- a Wi-Fi password changed since pairing;
- the firewall;
- the pager was revoked (NOT AUTHORISED).

To remove a pager from `devices.toml`, the person revokes it on the setup page, or runs `pager-bridge devices revoke <name>` themselves. You never do.
