# Pairing a pager

Pairing introduces a pager and a bridge to each other. Each learns the other's full public key, and the pager learns the Wi-Fi networks it may join and the address it dials. From then on, the bridge lets in only the pagers in its `devices.toml`, and a pager talks only to the bridge whose host key it pinned.

Pairing happens over USB only, never over the network, and it takes two confirmations: a press of BOOT on the pager, then yours on the setup page or the terminal. Nothing pairs a pager behind your back: not the setup page, not the Claude Code skill, not a process on the network.

## What you need

- The bridge installed, with its keys (`pager-bridge init`, or the setup page's first step makes them).
- The pager flashed, on a USB-C cable that carries data. `ls /dev/cu.usbmodem*` lists the port.
- The exact names of the Wi-Fi networks the pager may join, up to four, and their passwords. 2.4 GHz only: see [the hotspot guide](hotspot.md) for a phone's.
- A name for the pager: 1 to 24 of `A-Z a-z 0-9 . _ -`, such as `pager-01`.

## With the setup page

```sh
pager-bridge setup
```

1. **Give the networks,** highest priority first, each with its password. The page warns when a name is close to, but not the same as, one this Mac has saved (a curly apostrophe for a straight one, a case or a space): use the Mac's spelling unless you know better.
2. **Put the pager in pairing mode when the page asks for it,** within 2 minutes: the page waits that long for the pager's hello, and finds one already showing PAIR VIA USB at once. A pager that has never been paired, or was factory reset, starts in pairing mode and shows PAIR VIA USB. Any other: open its global menu (hold BOOT 1.5 s on the list, then choose SETTINGS; with no agents, the hold opens it straight away), choose **Pair again** and confirm it with BOOT. It keeps its key.
3. **Compare the fingerprints.** The page shows two: the pager's key (`DEV`) and the bridge's (`HOST`). The pager shows the same two under PAIR WITH *your Mac*? Check that both match, all the way through. So far the pager has been sent nothing secret.
4. **Press BOOT on the pager,** within 30 s. PWR cancels. It shows NOW CONFIRM ON *your Mac*, and waits 30 s.
5. **Confirm on the page:** "They match: send the Wi-Fi networks". Only now do the networks and their passwords go to the pager, which takes them without another press.

Only when the pager reports that it has saved the settings is it added to `devices.toml`. The page then shows the pager joining Wi-Fi and dialling the bridge, and the first page arrives.

## From the terminal

`pager-bridge provision` does the same, with the passwords on stdin, one line per network in `--wifi-ssid` order:

```sh
read -rs pass1 && read -rs pass2 && printf '%s\n%s\n' "$pass1" "$pass2" | pager-bridge provision \
    --port /dev/cu.usbmodemXXXX --name pager-01 --wifi-ssid "Home" --wifi-ssid "Sam’s iPhone" --wifi-pass-stdin
unset pass1 pass2
```

A pager paired before is put in pairing mode once `provision` has started, as in step 2 above. `provision` waits up to 120 s for the pager's hello (one already showing PAIR VIA USB is found at once), prints both fingerprints, and waits 30 s for BOOT. Then it asks on the terminal whether they match the pager's screen, and sends the networks only on `y` (its stdin carries the passwords, so it asks on `/dev/tty`). `--host <address>` sets the address the pager dials, if `<this Mac's LocalHostName>.local` doesn't resolve where it'll be. [`docs/bridge.md`](../bridge.md#provision-pair-a-pager) has every flag.

## What pairing guarantees

- **Full keys, both ways.** The bridge stores the pager's whole ed25519 public key, and the pager pins the bridge's whole host key. A fingerprint is only a label for you to compare; neither end ever authenticates on one.
- **Only ed25519.** A pager offering any other key type is refused.
- **Your confirmation, on the device and on the Mac.** The pager pairs only after a BOOT press while it shows both fingerprints, and the bridge sends its networks only after you confirm on the page or the terminal too.
- **No password before you confirm.** No Wi-Fi password reaches the pager before both fingerprints have been compared and confirmed, on both sides. Before that it has only the bridge's key, its name, the address it will dial and this Mac's name.
- **A password goes nowhere else.** It travels from the page, or `provision`'s stdin, through the bridge's memory and over USB to the pager. It's never written to a file, logged, shown, put in a URL or passed as a process's argument.
- **Nothing half done.** If anything fails (no answer from the pager, PWR, the 30 s passing, a refused key), `devices.toml` is unchanged.

## Pair again

Pair again to change the Wi-Fi networks, the address the pager dials, or the bridge it belongs to. Pairing gives the pager its whole list of networks again, so give every network it should keep.

On the setup page, that's **Change the Wi-Fi networks**: in Manage, and after pairing when the pager doesn't join. It keeps the pager's name, fills in the networks' names it was paired with on that page, and asks for each password again (nothing kept them). On the pager: hold BOOT for its menu, choose SETTINGS, then PAIR AGAIN, and BOOT for yes.

After pairing, while the pager is still on USB, the page reads its log as it restarts and says how its join went: joined, the password refused, or no network it can see.

- The same pager, with the same key, updates its entry rather than adding another.
- A name already paired with a different key is refused. Revoke the old one first, or choose another name.
- Pair again keeps the pager's key. To give it a new one, factory reset it first (below).

## Revoke a pager

```sh
pager-bridge devices revoke pager-01
```

Or **Revoke** in the setup page's Manage view. A running bridge closes the pager's session within 2 s and refuses it from then on; the pager shows NOT AUTHORISED and tries again every 5 minutes. `pager-bridge devices list` shows who's paired, with each full fingerprint.

## Factory reset

Hold **BOOT and PWR together for 10 s**. The pager asks FACTORY RESET?: BOOT resets, PWR cancels. It forgets its networks and its pairing, makes a new key, and starts in pairing mode. Revoke its old entry on the bridge, then pair it again.

## If it was lost, or plugged into a computer you don't trust

The pager's storage isn't encrypted, and a computer it's plugged into over USB can read its whole flash: its key and its Wi-Fi passwords. Treat it as lost:

1. Revoke it: `pager-bridge devices revoke <name>`.
2. Factory reset it, if you have it, so it makes a new key.
3. Pair it again.
4. Change the passwords of the Wi-Fi networks it knew.

## If the bridge's host key changes

Every paired pager pins `~/.config/herdr-pager/host_ed25519`. If it's replaced (a new Mac, a deleted folder), each pager shows HOST KEY CHANGED - RE-PAIR VIA USB, never authenticates to the new key, and checks again every 5 minutes. Pair each one again. Back up `host_ed25519` to keep your pairings across a reinstall, and keep the copy as safe as the original: anyone with it can pose as the bridge to your pagers.

## When pairing doesn't work

| What you see | What to do |
|---|---|
| The page or `provision` waits, and the pager doesn't show PAIR VIA USB | Choose **Pair again** in the pager's global menu. A pager showing NO DEVICE KEY needs a factory reset first. |
| `provision` can't open the port | Another program holds it: a serial monitor, or the browser's flasher. Close it, or unplug and plug the pager in again. |
| The fingerprints don't match | Press PWR to cancel. Something other than your pager, or your bridge, is answering: check the cable and which port you chose. |
| The 30 s pass | Nothing was paired. Start again. |
| The pager refuses the offer as invalid | Its firmware is older than the bridge. Flash the firmware shipped with the bridge (the setup page's Flash step), then pair. |
| The name warning | Use the spelling the Mac has saved, unless you're sure yours is the network's own. |
| Paired, then WRONG WIFI PASSWORD (WIFI *name* FAILED in the status bar) | That network refused the password. Change the Wi-Fi networks, and type it again. |
| Paired, then NO WIFI | It can see none of its networks: one that's 5 GHz only, out of range, or named differently all look the same to it. Change the Wi-Fi networks if a name is wrong. [The hotspot guide](hotspot.md) covers phones. |
| Paired, then NO BRIDGE ON THIS NETWORK | The Mac isn't on the network the pager joined, or the firewall blocks the bridge: `pager-bridge doctor`. |
