# A phone's hotspot

The pager can page you on a phone's hotspot as well as at home, as long as **the Mac is on the same hotspot**. The pager reaches the bridge only on a network it shares with the Mac: there's no relay, no cloud and no tailnet (`docs/adr/0005`). The desk pager is meant for the desk, on USB power; a hotspot is for when the desk moves, a café or a train, with the Mac and the pager both on the phone.

## Set up the phone

The ESP32-S3 joins only 2.4 GHz networks, and many phones offer their hotspot on 5 GHz alone unless told otherwise.

- **iPhone (12 or later):** Settings → Personal Hotspot. Turn on **Allow Others to Join** and **Maximize Compatibility**, which puts the hotspot on 2.4 GHz.
- **Android:** in the hotspot's settings, set its band to **2.4 GHz**. The setting's name and place differ from phone to phone (AP band, Wi-Fi hotspot band). herdr-pager has been tested on an iPhone's hotspot only.

An iPhone shows its hotspot to other devices only while **Settings → Personal Hotspot** is open on its screen. Keep that screen open until the pager has joined. Once joined, it stays joined.

## Get the name exactly right

A network's name must match exactly. An iPhone names its hotspot after the phone, usually with a curly apostrophe (`Sam’s iPhone`), and a straight one (`Sam's iPhone`) never matches it.

- Join the hotspot from the Mac first. Then the setup page, and `pager-bridge provision`, warn when the name you give is close to, but not the same as, one this Mac has saved, and give the Mac's spelling.
- To copy the exact name yourself: `networksetup -listpreferredwirelessnetworks en0`.

## Pair it with the hotspot

Pair the pager with its home network first and the hotspot second ([pairing](pairing.md)), so it uses the hotspot only where home isn't in range:

```sh
read -rs home && read -rs phone && printf '%s\n%s\n' "$home" "$phone" | pager-bridge provision \
    --port /dev/cu.usbmodemXXXX --name pager-01 --wifi-ssid "Home" --wifi-ssid "Sam’s iPhone" --wifi-pass-stdin
unset home phone
```

Or give both networks, in that order, on the setup page.

The pager dials the bridge at `<this Mac's LocalHostName>.local` (device info shows the address after BRIDGE). `.local` names resolved across an iPhone's hotspot when it was tested. If they don't on yours, pair again with `--host <the Mac's address on the hotspot>`; but a hotspot hands out addresses as it likes, so an address may change from one day to the next.

## Test it

Check the hotspot once, before you rely on it. The pager uses the hotspot only where it can't see a network higher on its list, and it stays on a network while that network is up, so at home it won't move to the hotspot on its own. Test away from home, or make the hotspot its first choice for the test:

1. **The phone:** turn the hotspot on, as in [Set up the phone](#set-up-the-phone), and keep **Settings → Personal Hotspot** open.
2. **The Mac:** join the hotspot from the Wi-Fi menu. The pager reaches the bridge only on a network it shares with the Mac.
3. **The pager:** away from home, just switch it on. At home, pair it again with the hotspot first ([pairing](pairing.md): on the setup page, Manage, then Change the Wi-Fi networks, with the hotspot at the top of the list). Pairing again is today's only way to change its networks.
4. **What you should see:** the status bar names the hotspot, the link connects, and your agents appear on the list. Device info (hold BOOT for the menu, then SETTINGS and DEVICE INFO; on an empty list with no link, PWR shows it) shows the pager's address on the hotspot and the bridge it dials.
5. **If it doesn't:** the table below says what each message means. `pager-bridge doctor` checks the Mac's side, the firewall included.
6. **Afterwards, at home:** pair it again with home first and the hotspot second, and put the Mac back on your home network.

## How it moves between networks

- While it isn't on Wi-Fi, the pager scans at least every 60 s and joins the saved network highest on its list that it can see.
- Once on a network, it stays there while that network is up, even if the Mac has moved to another. Back home with the hotspot still on, it stays on the hotspot: turn the hotspot off, and the pager finds home at its next scan, within a minute or so.
- A network that refuses it three times in a row shows as WIFI *name* FAILED in the status bar, which is most often a wrong password.

## What the pager says

| The pager shows | What it means | What to do |
|---|---|---|
| NO WIFI, Looking for a saved network | It can't see any of its saved networks | On an iPhone, open Settings → Personal Hotspot and keep it open; check Maximize Compatibility |
| WIFI *name* FAILED | The network refused it three times in a row | Check the password, and pair again |
| CONNECTING, to the bridge at *address* | It's on Wi-Fi and dialling the bridge | Wait a few seconds |
| NO BRIDGE ON THIS NETWORK, Is the Mac on *name* too? | It's on the hotspot but can't reach the bridge | Put the Mac on the same hotspot; check `pager-bridge doctor` (the firewall); PWR shows device info, with the address it dials |

The pager pages only while it's connected to the bridge. With the link down, it never pages from an old list: the list's title says how old it is.
