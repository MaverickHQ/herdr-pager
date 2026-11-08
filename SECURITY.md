# Security

## Reporting a vulnerability

Report it privately, through GitHub: the repository's **Security** tab, then **Report a vulnerability**. Please don't open a public issue for it. herdr-pager is one person's tool, so expect an answer within a week or two, not hours.

Say what you found, which part it's in (the pager's firmware, the bridge or the pairing), what someone could do with it, and how to reproduce it, ideally with the bridge's log and the firmware's commit.

## What counts

The pager is an **attention device, not a security gate** (`README.md` §Safety). Any process in a herdr pane can already drive herdr's socket and answer its own prompts, so "an agent can approve itself without the pager" is how herdr works, not a vulnerability here.

These are in scope:

- anything that gets the bridge to send herdr a key it wasn't asked for, a key outside its keymap, or any text;
- an approval that lands on a prompt other than the one on the card, despite the `state_change_seq` and card-hash check;
- getting past either end's key check: a pager or host that isn't paired completing an SSH session;
- pairing a pager without the owner confirming it on the device, or pairing over anything but USB;
- a verdict that is queued, resent or replayed;
- agent or device text reaching a subprocess as an argument, or the bridge calling a herdr method outside its list (`docs/bridge.md`);
- crashing or hanging the bridge from the network before a session is authenticated.

These are known, and out of scope:

- the pager's storage isn't encrypted: someone holding the board, or any computer it's plugged into over USB, can read its Wi-Fi passwords and its key. Plug it only into computers you trust; `README.md` §Safety says what to do when one is lost or was plugged into one you don't trust (revoke it, factory reset it, pair it again, and change the Wi-Fi passwords). Flash and NVS encryption and secure download mode are planned for the portable pager;
- the danger rules on the cards are a reading aid and miss obfuscated commands;
- anything that needs a process already running as the Mac's user.

## Supported versions

The latest release, and the latest commit on `main`.
