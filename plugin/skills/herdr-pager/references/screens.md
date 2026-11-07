# The pager's screens and codes

The pager's screen is a 640 × 172 bar. Along the bottom runs the status band, with a hint cell at its right end under each button: PWR, then BOOT. Whatever a button does right now is written in its cell, so ask the person what the cells say when you aren't sure which screen they're on. Messages are in capitals, exactly as below.

## The list

One row per agent, the most urgent at the top. Each row shows the agent's herdr tab (`<workspace>.<tab> <label>`) and its folder. Its first character is its state:

| Code | Means |
|:---:|---|
| `!` | Blocked: it needs you, usually a permission prompt |
| `*` | Done: its turn is over (herdr calls every finished turn done, including one that ends by asking something) |
| `>` | Working |
| `?` | Unknown: herdr can't tell |
| `.` | Idle |

The pager pages when an agent has been blocked for 3 s: a beep, the screen lights up, and the row is selected. It reminds every 5 minutes, and goes quiet if the person answers at the keyboard. A done turn in a pane the person isn't watching plays a short "ta-da" once; DONE TONE in the settings turns that sound off.

When the list is empty, its middle says why:

| It says | Means | What fixes it |
|---|---|---|
| NO AGENTS | Connected, and herdr has no agents | Nothing |
| WAITING FOR HERDR | Connected; the bridge hasn't sent the first list yet | It should pass in a second or two. If it stays, run the doctor |
| HERDR OFFLINE / Is herdr running on the Mac? | Connected, but the bridge can't reach herdr | Start herdr; the doctor's `herdr` check |
| WRONG WIFI PASSWORD / `<network>` refused it: pair again in SETTINGS | A saved network refused its password three times in a row | Pair again with the right password: the setup page's Change the Wi-Fi networks |
| NO WIFI / Looking for a saved network | None of its saved networks is in range; it scans every 60 s | A 2.4 GHz network it knows, switched on. Or pair again with another network |
| CONNECTING / to the bridge at `<host>` | On Wi-Fi and dialling the bridge | It passes in seconds when all's well |
| NO BRIDGE ON THIS NETWORK / Is the Mac on `<network>` too? | A dial on this network ended without connecting | See below |

**NO BRIDGE ON THIS NETWORK** is the usual "it won't connect". Work through it in this order:

1. Is the Mac on the same network? On a phone's hotspot, the Mac must be on that hotspot too. An iPhone shows its hotspot to others only while Settings → Personal Hotspot is open, and needs Maximize Compatibility on for 2.4 GHz.
2. Run the doctor: the bridge must be running (`login`, `devices`), the firewall must let it in (`firewall`), and nothing else may hold port 2277 (`port`).
3. Does the Mac's `.local` name resolve on this network? Some networks block it. Pairing again lets the person give the Mac's IP address instead.

PWR on this screen shows device info, with the address the pager dials.

## The status band

From the left:

- **Counts:** `!2 *1 >3` is 2 blocked, 1 done and 3 working; `+N` means N agents beyond the 16 it shows. ALL CLEAR `>N` means nothing is blocked or done unseen, and N agents are working. On an open card, the agent's name stands here instead.
- **Flags,** when set: HERDR OFFLINE (the bridge can't reach herdr), ON BATTERY (the USB cable is out; the desk pager is meant to run on USB power), SILENT (silent mode is on).
- **The clock.**
- **The link:**

  | Glyph | Means |
  |:---:|---|
  | `o` | Connected to the bridge |
  | `~` | Not connected, or dialling |
  | `x` | The bridge refused its key: NOT AUTHORISED |
  | `#` | The bridge's host key changed: HOST KEY CHANGED |

- **The network:** its name when joined. NO WIFI when none is in range. WIFI `<name>` FAILED when it found the network but couldn't join it, most likely a wrong password. A wrong password is fixed by pairing again, with the password typed on the setup page.
- **LAST SEEN `<age>`,** on the list while the link is down: how old the list on screen is. It's the list from before the drop, not live.

## Screens that stop the pager

| It shows | Means | What fixes it |
|---|---|---|
| PAIR VIA USB | It has never been paired, or was factory reset. Its line still names `pager-bridge provision`; the setup page does the same | Pair it on the setup page |
| NO DEVICE KEY | It has no usable key, so it can't pair or dial. Its lines say: hold BOOT and PWR 10 s to reset, then pair it again from the Mac | A factory reset (below), then pair it on the setup page |
| NOT AUTHORISED | The bridge refused its key: it was revoked, or `devices.toml` lost it. It tries again every 5 minutes | Pair it again on the setup page |
| HOST KEY CHANGED - RE-PAIR VIA USB | Whatever answered isn't the bridge it paired with: the bridge's `host_ed25519` was replaced, or another machine answered. It never logs in to that key. It tries again every 5 minutes, and connects if its own bridge answers | If the person replaced the key, pair it again. If not, treat it as a warning, and find out what answered first |

On NOT AUTHORISED and HOST KEY CHANGED, PWR shows device info and a BOOT hold opens the global menu, where PAIR AGAIN is. On PAIR VIA USB and NO DEVICE KEY the band is blank and the buttons do nothing, except the factory reset: hold BOOT and PWR 10 s.

## Pairing

Pairing happens on the setup page, over USB.

1. The page opens the pager's port, which resets the board.
2. A pager that's already paired must be put in pairing mode now: Pair again in its global menu (it asks PAIR AGAIN?).
3. The pager shows PAIR WITH `<host>`?, with two full fingerprints: HOST (the bridge's key) and DEV (the pager's own).
4. The person checks that both match what the page shows, then presses **BOOT** within 30 s. PWR, or 30 s passing, cancels. So far the pager has been sent nothing secret.
5. The pager shows NOW CONFIRM ON `<host>`, with the same two fingerprints, and waits 30 s. The person confirms on the page ("They match: send the Wi-Fi networks"); only then do the networks and their passwords go to the pager, which takes them without another press. PWR still cancels.
6. Only then is the pager paired. It restarts, joins its Wi-Fi and dials the bridge.

A pager that refuses the offer as invalid has firmware older than the bridge: the setup page's Flash step puts the shipped firmware on it first.

Pair again keeps the pager's key. A factory reset makes a new one.

## Cards

PWR on a row opens that agent's card.

| Card | Its hints | What to do |
|---|---|---|
| A permission card: the first line is what the agent asks to do (the command, the file path, the URL with its host on the next line, or the MCP tool with each argument on a line) | PWR DENY, BOOT HOLD: APPROVE | Hold BOOT 1 s to approve: a bar fills under BOOT, and letting go early sends nothing. PWR denies. A BOOT press goes back with no verdict |
| DANGER: `<rule>` over a permission card (a force push, `sudo`, `curl … \| sh`, a secret file, a local address, …) | HOLD 2S: APPROVE | Read it twice. Approving takes a 2 s hold. No banner doesn't mean safe: the rules miss what they don't know |
| COMMAND NOT READ, FILE NOT READ, URL NOT READ or TOOL NOT READ | PWR DENY, BOOT APPROVE: KEYBOARD | The bridge couldn't read the prompt safely, so the pager can only deny it. Approve it at the keyboard |
| QUESTION - ANSWER AT KEYBOARD | BACK, HOLD: MENU | Not a permission prompt. Answer it at the keyboard |
| WAITING - ANSWER AT KEYBOARD | BACK, HOLD: MENU | Blocked, but the bridge couldn't find its prompt on screen. Answer it at the keyboard |
| NOTHING NEEDS YOU | BACK, HOLD: MENU | The agent isn't asking anything. Under it: what it's doing and for how long |

Touch never approves, denies or wakes anything. A card must be on screen for 500 ms before a hold counts. A verdict is never queued or sent again.

## Messages that come and go

| It says | Means |
|---|---|
| OK | The verdict or interrupt was sent |
| SENT, in a hint cell | The verdict is on its way to the bridge |
| SCREEN CHANGED | The prompt changed before the keys went out, so nothing was sent. The card is fetched again as it is now: read it again |
| JUMPED | The agent's pane is in front in herdr |
| X NO LINK | No link to the bridge, so nothing was sent. See the status band's link |
| X LINK LOST | The link dropped while a verdict was on its way. It wasn't confirmed, and is never resent: check the agent at the keyboard |
| X GONE | herdr no longer has that pane |
| X NOT ALLOWED | The agent's kind has no keys for that verb (only Claude Code has a keymap), or the card doesn't offer it |
| X HERDR OFFLINE | The bridge couldn't reach herdr just then |
| X HERDR ERROR | herdr answered with an error |
| X BAD REQUEST | The bridge didn't understand the pager. Usually the pager and the bridge are from different versions: update both |
| X RATE LIMITED | Too many presses too fast; try again |
| SILENT ON, SILENT OFF | Silent mode, toggled by holding PWR 2 s |

## Menus

- **The agent's menu** (hold BOOT 1.5 s on the list): JUMP (bring its pane to the front in herdr), INTERRUPT (only while it's working; it asks INTERRUPT `<agent>`?, and PWR means yes, so a reflexive BOOT can't interrupt), and SETTINGS.
- **SETTINGS,** the global menu (a BOOT hold opens it straight away on an empty list, on NOT AUTHORISED and on HOST KEY CHANGED): silent mode, VOLUME, DONE TONE, BRIGHTNESS, COLOUR (ORANGE or GREEN), DEVICE INFO, PAIR AGAIN.
- In a menu: BOOT moves to the next item, PWR selects it, and holding BOOT 1.5 s closes the menu.

## Device info

Opened from SETTINGS, with PWR on an empty list while the link is down, or with PWR on NOT AUTHORISED or HOST KEY CHANGED:

| Line | Means |
|---|---|
| FW `<version>` STARTED: `<why>` | The firmware's version (`-dev` for a build that isn't a published version), and why it last started: POWER ON, RESTART, CRASH, BROWNOUT (the power dipped: try another cable or port) or RESET. NO SOUND means the speaker failed to start |
| DEV `<fingerprint>` | The pager's own key: the fingerprint the doctor lists for it |
| HOST `<fingerprint>` | The bridge's key it pinned when paired |
| IP `<ip>` BRIDGE `<host>:<port>` | Its address, and the bridge it dials |

The pager's firmware and the bridge should be the same version. The setup page's Manage view compares them and updates the pager without losing its pairing.

## The buttons, in short

| On | Press | Does |
|---|---|---|
| Anything, screen off | either | Wakes the screen, and nothing else |
| The list | BOOT | Next agent |
| The list | PWR | Opens the agent's card |
| The list | hold BOOT 1.5 s | The agent's menu |
| A permission card | hold BOOT 1 s (2 s on DANGER) | Approves |
| A permission card | PWR | Denies |
| The list or a card | hold PWR 2 s | Silent mode on or off |
| Any time | hold BOOT and PWR 10 s | FACTORY RESET? BOOT resets, PWR cancels |

**A factory reset** erases the pager's pairing, its Wi-Fi networks and its key, and makes a new key. Afterwards it shows PAIR VIA USB. Revoke the old pairing on the setup page, then pair it again there.

**A lost pager, or one plugged into a computer the person doesn't trust:** its storage isn't encrypted, so its key and Wi-Fi passwords could be read. The person revokes it on the setup page, factory resets it if they have it, pairs it again, and changes the passwords of the Wi-Fi networks it knew. You explain; they do it.
