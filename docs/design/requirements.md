# Requirements Document

## Introduction

herdr-pager turns a Waveshare ESP32-S3-Touch-LCD-3.49B into a 1990s-style pager for [herdr](https://github.com/herdrdev/herdr), the terminal runtime that runs coding agents and classifies each pane as `idle`, `working`, `blocked`, `done` or `unknown`. The pager:

- shows which agents need attention;
- beeps when one is blocked;
- lets the user approve, deny, interrupt, snooze or jump to an agent from physical buttons, with touch for browsing;
- works wherever it shares a network with the host: home or office Wi-Fi, or a phone hotspot the host has also joined. (There is no tailnet: ADR 0005.)

The system has two parts:

1. **pager firmware** on the ESP32-S3 (C/C++, ESP-IDF).
2. **pager-bridge**, a single Go binary on the host running herdr. It runs an SSH server that only the pager's key can use, and it is the only component that talks to herdr's local JSON socket.

The device always dials out. All transport security is SSH. Approvals are delivered to herdr as keystrokes, guarded by a check that the screen the user saw is still current.

The pager is an attention and remote-control device, **not a security gate**. Any process inside a herdr pane can already drive `herdr.sock` (see design.md §Threat model). Evidence for every design choice is in `reports/ESP32 agent pager prior art.md` and `research_notes/ESP32 agent pager prior art/`.

Terms used throughout:

- **Card:** the detail view for one agent.
- **Page:** an audible or visible alert.
- **Ack:** opening the card for a page.
- **Seen:** a `done` agent whose card the user opened on the pager.
- **Keymap:** the per-agent mapping from verbs (approve, deny, interrupt) to herdr key names.

## Requirements

### Requirement 1: Bridge setup and identity

**User Story:** As the host owner, I want a one-command bridge setup that creates its SSH identity and config, so that I can pair a pager without touching system SSH settings.

#### Acceptance Criteria

1. WHEN the user runs `pager-bridge init` THEN the system SHALL create `~/.config/herdr-pager/` with mode 0700, generate an ed25519 SSH host key at `host_ed25519` with mode 0600, write a default `config.toml`, and write an empty `devices.toml`.
2. WHEN `pager-bridge init` completes THEN the system SHALL print the host key's full SHA256 fingerprint and the configured listen address.
3. WHEN `pager-bridge init` is run a second time THEN the system SHALL change no existing file and SHALL print the same fingerprint.
4. THE SYSTEM SHALL NOT require macOS Remote Login, the system `sshd`, or root privileges.
5. IF `herdr` is not reachable at the configured socket path THEN `pager-bridge init` SHALL still succeed and SHALL print a warning naming the socket path it tried.

### Requirement 2: Pairing a pager over USB

**User Story:** As the host owner, I want to pair the pager over a USB cable and confirm it on the device, so that pairing never opens a network listener and never trusts a key I haven't seen.

#### Acceptance Criteria

1. WHEN the firmware boots with no saved configuration, or after a factory reset, THEN the device SHALL generate its own SSH key pair on the device, enter provisioning mode, and display "PAIR VIA USB".
2. WHEN the user holds BOOT and PWR together for 10 seconds while the firmware is running THEN the device SHALL ask "FACTORY RESET?", with BOOT meaning reset and PWR meaning cancel, each meaning shown under its button (amended 2026-10-22, phase 2's grilling). On BOOT it SHALL erase its configuration and key pair and enter provisioning mode. (A boot-time combination is impossible because holding BOOT, GPIO0, at reset enters the ROM download mode.)
3. WHEN the user runs `pager-bridge provision --port <serial> --name <device-name> --wifi-ssid <ssid> [--host <hostname-or-ip>]` THEN the tool SHALL read the Wi-Fi password from an interactive prompt or from stdin (`--wifi-pass-stdin`), and SHALL NOT accept it as a command-line argument.
4. WHEN the tool connects THEN it SHALL obtain the device's full public key over USB serial, validate that it parses as an ed25519 SSH public key (design D4; any other type is refused with a precise error (amended 2026-10-22, phase 2's grilling)), and send the device the Wi-Fi settings, host address, port, and the bridge's **full** host public key.
5. WHEN the device receives provisioning data THEN it SHALL display the full SHA256 fingerprints of both the host key and its own key, the host name, and that BOOT pairs and PWR cancels, each meaning shown under its button (amended 2026-10-22, phase 2's grilling), and the tool SHALL print the same two fingerprints.
6. WHEN the user presses BOOT within 30 seconds THEN the device SHALL persist the configuration and report success, and only then SHALL the tool add the device's full public key and name to `devices.toml`.
7. IF the user presses PWR or 30 seconds elapse THEN the device SHALL discard the provisioning data, the tool SHALL exit non-zero, and `devices.toml` SHALL be unchanged.
8. THE device's private key SHALL never be transmitted off the device, over USB or any other channel.
9. IF `--host` is omitted THEN the tool SHALL default to the host's mDNS name (`<LocalHostName>.local`), and the device SHALL resolve `.local` names via mDNS.
10. WHEN the user selects **Pair again** in the global menu THEN the device SHALL enter provisioning mode, keeping its existing key pair.

### Requirement 3: Device connection and authentication

**User Story:** As the host owner, I want the pager to reach the bridge over SSH with full-key authentication in both directions, so that nothing else can talk to the bridge and the pager can't be fooled by an impostor host.

#### Acceptance Criteria

1. WHEN the device has a saved configuration and Wi-Fi is up THEN it SHALL open an SSH connection to the configured host and port, authenticate with its own key, and open one session channel.
2. WHEN a client connects THEN the bridge SHALL accept it only if the offered public key is byte-for-byte equal to a key in `devices.toml`. It SHALL offer no password or keyboard-interactive authentication.
3. WHEN the device connects THEN it SHALL compare the server's host key byte-for-byte with the pinned key. IF it differs THEN the device SHALL abort before authenticating, show "HOST KEY CHANGED - RE-PAIR VIA USB" (ASCII on screen (amended 2026-10-22, phase 2's grilling)), and SHALL NOT retry more often than every 5 minutes; WHEN a later dial finds the pinned key THEN it SHALL clear that state and connect (amended 2026-11-01, the security pass: a terminal state let one spoofed reply stop the pager until it was power-cycled).
4. IF the connection fails or drops THEN the device SHALL retry with exponential backoff from 1 s up to 30 s with jitter, and SHALL show the link state in the status bar (connected / connecting / auth rejected / host key changed).
5. IF authentication is rejected THEN the device SHALL show "NOT AUTHORISED" and retry no more often than every 5 minutes.
6. WHEN a second session arrives for the same device key THEN the bridge SHALL close the older session.
7. WHEN a key is rejected THEN the bridge SHALL log the offered key's full SHA256 fingerprint and the source address, and SHALL log no other client-supplied data.
8. WHEN the bridge receives any channel type other than `session`, or a `pty-req`, `env`, `subsystem`, `x11-req` or any forwarding request, THEN it SHALL reject the request.
9. IF a connection hasn't asked for its session (an `exec` or `shell` request) within 15 s of the bridge accepting it, whether its SSH handshake stalled or it authenticated and went quiet, THEN the bridge SHALL close it. WHEN the bridge stops THEN it SHALL close connections still in their handshake. (Added 2026-10-27: the owner's decision, open-items O-47. A pager takes under 2 s, its own SSH timeout is 10 s, and a power-saving network adds about 1 s a message.)

### Requirement 4: Monitoring herdr

**User Story:** As a pager user, I want the pager to reflect herdr's agent states within a second or two, so that what I see on my desk matches the console.

#### Acceptance Criteria

1. WHILE herdr is reachable THE bridge SHALL call `agent.list` on herdr's socket at least once per second and SHALL send a snapshot to each connected device whenever an agent's `agent_status`, `state_change_seq`, `completion_seq` or display name changes, or an agent appears or disappears.
2. WHEN a device completes the hello handshake THEN the bridge SHALL send a full snapshot within 1 second.
3. Each snapshot entry SHALL include:
   - the pane id;
   - a display name (at most 24 characters, sanitized per Requirement 13);
   - the agent kind;
   - the status;
   - `state_change_seq` and `completion_seq`;
   - the time the bridge first observed the current `state_change_seq`;
   - the set of verbs currently available for that agent.
4. IF herdr's socket is unreachable or returns an error THEN the bridge SHALL send an `offline` notice, the device SHALL show "HERDR OFFLINE", and the bridge SHALL resume normal snapshots within 2 seconds of herdr becoming reachable.
5. THE bridge SHALL NOT stream terminal frames or full scrollback to the device. Terminal text SHALL reach the device only as a card (Requirement 7).
6. Each snapshot SHALL carry the bridge's current Unix time, and the device SHALL set its clock (and RTC) from it when the drift exceeds 2 seconds.

### Requirement 5: Agent list (the pager face)

**User Story:** As a pager user, I want a glanceable list with the most urgent agent on top, so that I know what needs me without reading anything.

#### Acceptance Criteria

1. WHEN a snapshot is received THEN the device SHALL sort agents blocked > done (unseen) > unknown > working > idle, breaking ties by oldest state change first.
2. THE status bar SHALL show:
   - counts of blocked, unseen-done and working agents; WHILE the list is live (linked, with herdr answering) and has agents but none blocked and none done unseen, "ALL CLEAR" in place of the first two counts (amended 2026-11-01 by the owner, open-items O-85);
   - the time (HH:MM);
   - battery level;
   - link state.
3. EACH agent row SHALL show a state glyph, the display name, where the agent works (its folder, else the agent kind), and the age of the current state ("3m", "2h") (amended 2026-10-24 by the owner, at phase 2's desk checkpoint: phase 2 design D2-15).
4. THE device SHALL render the UI in landscape (640×172) with a monochrome 1990s LCD theme: a single foreground colour on a dark background, monospace pixel font, segment-style digits for the clock and counts (amended 2026-10-27, the owner's decision, open-items O-50: the built-in Montserrat stays, and the owner's dark-on-orange palette, parent design §UI, stands for the theme).
5. WHEN more agents exist than fit on screen THEN BOOT short-press SHALL move the selection down the list, wrapping at the end, and the view SHALL scroll to keep the selection visible. Touch scrolling is in Requirement 15.
6. Viewing or scrolling the list SHALL NOT mark anything seen or acknowledged (preview is not read).
7. Agents in `unknown` status SHALL render with a distinct glyph and SHALL NOT be shown as idle.

### Requirement 6: Paging (alerts)

**User Story:** As a pager user, I want the pager to beep when an agent is blocked and chirp when one finishes, so that I don't have to watch the console.

#### Acceptance Criteria

1. WHEN an agent has been `blocked` with the same `state_change_seq` for 3 seconds THEN the device SHALL page:
   - play the "blocked" tone pattern;
   - turn the backlight on;
   - flash the screen three times;
   - highlight that agent's row.
2. WHEN an agent's `completion_seq` increases THEN the device SHALL play the distinct "done" tone pattern once and SHALL NOT repeat it.
3. WHILE a blocked page is not acknowledged and not snoozed THE device SHALL repeat the page every 5 minutes.
4. WHEN a blocked agent leaves `blocked` (answered elsewhere, or its state changed) THEN the device SHALL cancel that page and its reminders without user action.
5. THE device SHALL page at most once per (pane id, `state_change_seq`) pair, including across reconnects.
6. WHILE silent mode is on, or the current time is inside the configured quiet hours, THE device SHALL suppress all sound and screen flashing and SHALL still update the list and counts.
7. WHEN the user holds PWR for 2 seconds (and less than 5) THEN the device SHALL toggle silent mode and show a confirmation for 2 seconds.
8. WHILE a page is unacknowledged THE backlight SHALL stay on for at most 60 seconds. After that, the status bar's blocked count SHALL be the persistent indicator.

### Requirement 7: Cards, acknowledgement and seen state

**User Story:** As a pager user, I want to open an agent to read what it's asking before I act, so that I never approve blind.

#### Acceptance Criteria

1. WHEN the user short-presses PWR on the list THEN the device SHALL request a card for the selected agent and display it when it arrives.
2. FOR a blocked agent, the card SHALL show:
   - a one-line summary;
   - up to 3 lines (an MCP call's, up to 4: its server and tool, then one per argument; amended 2026-11-02, O-90's review) (each at most 40 characters) taken from herdr's `detection` read of that pane, sanitized and with the agent's numbered option menu removed;
   - button hints for the available verbs.
   FOR any other agent, or a blocked agent whose prompt can't be read, a card with no text of its own SHALL say that nothing needs the user (for a blocked agent, to answer at the keyboard), what the agent is doing and for how long, and its kind and where it works. A card that takes no verdict SHALL show the hints BACK and HOLD: MENU (amended 2026-11-01 by the owner, open-items O-82: OPEN on a working agent showed a blank screen).
3. The bridge SHALL compute a hash over the exact sanitized card text it sent, and over the raw command or path the keymap read, in full and unsanitized, and include it, together with the agent's `state_change_seq`, in the card (amended 2026-11-01 by the owner, open-items O-80).
4. WHEN a card for a blocked agent is displayed THEN the device SHALL mark that (pane, `state_change_seq`) acknowledged and stop its reminders.
5. WHEN a card for a `done` agent is displayed THEN the device SHALL mark that (pane, `completion_seq`) seen locally, and SHALL NOT call `agent.focus`.
6. IF no button is pressed and no touch arrives for 60 seconds while a card is open THEN the device SHALL return to the list and send no verdict.
7. WHEN the detection text does not match the agent's keymap `permission_match` patterns (for example a multiple-choice question rather than a permission prompt) THEN the card SHALL show "QUESTION - ANSWER AT KEYBOARD" and SHALL NOT offer approve or deny (amended 2026-10-22, phase 2's grilling).

### Requirement 8: Approve and deny

**User Story:** As a pager user, I want to approve or deny a blocked agent's permission prompt from physical buttons, so that I can unblock work from across the room or from my pocket, but only for the exact prompt I read and never by accident.

#### Acceptance Criteria

1. WHILE a card offering approve/deny has been on screen for at least 500 ms THE device SHALL send `approve` when BOOT has been held for 1 second, and `deny` on PWR short-press. While BOOT is held, a fill bar SHALL show progress, and releasing before 1 second SHALL send nothing. BOOT short-press on such a card SHALL return to the list without a verdict. Each verdict SHALL carry the pane id, the card's `state_change_seq` and the card hash. A permission card that failed closed (18.1) offers deny alone: PWR short-press SHALL send `deny`, BOOT short-press SHALL return to the list, and no BOOT hold SHALL send anything; BOOT's hint SHALL say to approve at the keyboard (amended 2026-11-01 by the owner, open-items O-80).
2. Approve and deny SHALL be possible only from the physical buttons, never from touch.
3. WHEN the bridge receives a verdict THEN, immediately before sending any keys, it SHALL re-read the agent and its detection text and SHALL send keys only if:
   - the status is still `blocked`;
   - the `state_change_seq` matches;
   - the recomputed card hash matches.
4. IF any check in criterion 3 fails THEN the bridge SHALL send no keys and SHALL reply `stale`. The device SHALL show "SCREEN CHANGED" for 2 seconds and re-request the card, and SHALL NOT resend the verdict.
5. WHEN checks pass THEN the bridge SHALL call `agent.send_keys` with exactly the keymap's key list for that verb and agent kind. It SHALL reply `ok` or `error`, and the device SHALL show "OK" or "X" for 2 seconds (ASCII on screen (amended 2026-10-22, phase 2's grilling)).
6. Approve and deny SHALL be offered only for agents whose kind has a keymap. Agents without one SHALL offer only `jump`. A permission card that failed closed (18.1) SHALL offer `deny` and `jump`, never `approve`, and the bridge SHALL answer an `approve` on it `not_allowed` and send nothing (amended 2026-11-01 by the owner, open-items O-80).
7. IF the SSH link drops between a button press and a reply THEN the device SHALL discard the verdict and SHALL NOT resend it after reconnecting.
8. THE device SHALL NOT offer "always allow" or "don't ask again" options.

### Requirement 9: Menu — snooze, jump, interrupt

**User Story:** As a pager user, I want a small menu for the less common actions, so that the two buttons stay simple.

#### Acceptance Criteria

1. WHEN the user holds BOOT for 1.5 seconds on the list or on a card without verdicts THEN the device SHALL open the menu for the selected agent (or for the global settings if no agent is selected). On a permission card the BOOT hold approves instead (8.1).
2. IN the menu, BOOT short-press SHALL move to the next item, PWR short-press SHALL select, and holding BOOT for 1.5 seconds SHALL close the menu. Touch MAY also select items (Requirement 15).
3. WHEN the user selects **Snooze 10 min** THEN the device SHALL suppress reminders for that agent for 10 minutes and SHALL page again when the snooze ends if the agent is still blocked with the same `state_change_seq`.
4. WHEN the user selects **Jump** THEN the device SHALL send `jump`, and the bridge SHALL call `agent.focus` for that pane. The device SHALL show the result: JUMPED, or X and the reason (amended 2026-11-01 by the owner, open-items O-83).
5. WHEN the user selects **Interrupt** THEN the device SHALL ask "INTERRUPT <name>?", with PWR meaning yes and BOOT meaning no, each meaning shown under its button. Only on PWR SHALL it send `interrupt`, which the bridge SHALL translate using the keymap's interrupt keys. Interrupt SHALL be offered only for agents with a keymap, and only while the agent is working: on a blocked prompt the interrupt key is a deny that would skip the stale check (amended 2026-10-22, phase 2's grilling).
6. The global menu SHALL include silent mode, quiet hours on/off, volume, the done tone on/off (amended 2026-10-25 by the owner, on phase 2's acceptance day), brightness, the colour (amended 2026-10-24 by the owner, at phase 2's desk checkpoint: phase 2 design D2-15), device info (name, firmware version and why the device last started, both key fingerprints, IP and the bridge's address: the last two amended 2026-10-27 by the owner, open-items O-77), Pair again, and power off.
7. WHILE BOOT and PWR are held together THE device SHALL suppress every single-button hold action (menu, silent toggle, power-off countdown) and treat the press as the factory-reset combination (2.2).

### Requirement 10: Device power

**User Story:** As a pager user, I want the pager to last as long as possible on battery and to turn on and off like a real device.

#### Acceptance Criteria

1. WHEN the firmware starts THEN it SHALL assert the power-hold line (TCA9554 P6) before initialising the display.
2. WHEN the user holds PWR for 5 seconds THEN the device SHALL show a 3-2-1 countdown and, if PWR is still held, release the power-hold line. On USB power, where the device can't switch off, it SHALL instead turn the screen off and show "USB POWER" on next wake.
3. WHEN no button is pressed for 30 seconds and no page is active THEN the device SHALL turn the backlight off and stop UI rendering and touch polling.
4. WHILE the backlight is off THE device SHALL use Wi-Fi modem power save and automatic light sleep, and SHALL wake on BOOT or PWR.
5. THE device SHALL measure battery voltage (ADC1 channel 3, ×3 divider), show a battery level in the status bar, and page once with the "low battery" pattern below 3.5 V.
6. WHEN the device wakes from light sleep with the SSH session still open THEN it SHALL NOT reconnect. WHEN the session has dropped THEN it SHALL reconnect and use the fresh snapshot.
7. WHEN a button press turns the backlight on THEN that press SHALL only wake the screen and SHALL NOT be routed as an action (pocket safety).

### Requirement 11: Bridge operation

**User Story:** As the host owner, I want to run, inspect and test the bridge with ordinary commands, so that it behaves like a well-mannered daemon.

#### Acceptance Criteria

1. WHEN the user runs `pager-bridge serve` THEN the bridge SHALL listen on the configured address (default `0.0.0.0:2277`), log to stderr, and run until SIGINT or SIGTERM, on which it SHALL close sessions and exit 0 within 2 seconds.
2. WHEN the user runs `pager-bridge status` THEN it SHALL print whether a bridge is listening, the herdr socket state, the paired devices, and which are connected. `--json` SHALL emit the same data as JSON.
3. WHEN the user runs `pager-bridge stdio` THEN the bridge SHALL speak the pager protocol on stdin/stdout for one session, with identical behaviour to an SSH session (for testing, and for future use as an OpenSSH forced command).
4. THE repository SHALL include a launchd user-agent plist and documented `launchctl` commands to run `serve` at login.
5. THE bridge SHALL NOT log card text, detection text or agent names at default verbosity. It SHALL log pane ids, verbs, results and timings.
6. EVERY command SHALL be non-interactive when given flags or stdin (scriptable), except the on-device confirmation in Requirement 2.

### Requirement 12: Device management

**User Story:** As the host owner, I want to list and revoke paired pagers, so that a lost pager stops working immediately.

#### Acceptance Criteria

1. WHEN the user runs `pager-bridge devices list` THEN it SHALL print each device's name, key type, full SHA256 fingerprint, pairing date and connection state.
2. WHEN the user runs `pager-bridge devices revoke <name>` THEN it SHALL remove the device from `devices.toml`, and a running bridge SHALL close that device's session within 2 seconds.
3. `devices.toml` writes SHALL be atomic (write a temporary file, then rename) with mode 0600.

### Requirement 13: Security constraints (cross-cutting)

**User Story:** As the host owner, I want the bridge to give a pager no more power than a person pressing one key at my keyboard, so that a lost, stolen or confused pager can't become a shell.

#### Acceptance Criteria

1. THE bridge SHALL accept from devices only these message types: `hello`, `ping`, `list`, `show`, `approve`, `deny`, `interrupt`, `jump`. Any other type SHALL get no reply: it SHALL be logged (its type only, never its content) and counted as an invalid message (amended 2026-10-22, phase 2's grilling: the protocol has no `error` message to reply with). Five invalid messages within one session SHALL close it.
2. THE bridge SHALL send herdr only keys taken from its keymap. It SHALL NOT forward any key, text or prompt supplied by a device, and SHALL NOT call `pane.send_text`, `pane.send_input`, `agent.prompt` or any method outside `agent.list`, `agent.get`, `agent.read`, `agent.send_keys`, `agent.focus`, and the read-only `tab.list` and `workspace.list` (amended 2026-10-24 by the owner, at phase 2's desk checkpoint: they give the tab names the pager's list shows).
3. THE bridge SHALL NOT pass agent-derived or device-derived strings as arguments to any subprocess. All herdr interaction SHALL go through the JSON socket.
4. ALL text sent to the device SHALL be sanitized:
   - ANSI/escape sequences, C0/C1 controls, bidi overrides and isolates, and zero-width characters removed;
   - common symbols transliterated to ASCII, and other non-ASCII characters replaced with `?`;
   - whitespace folded;
   - each field truncated head-and-tail with `..` to its limit; names and folders are cut at their end instead (amended 2026-10-24 by the owner, at phase 2's desk checkpoint: phase 2 design D2-15).
5. THE bridge SHALL close any session that sends a line longer than 512 bytes. The device SHALL discard any received line longer than 8,192 bytes. Neither limit counts the line's `\n` (amended 2026-10-22, phase 2's grilling).
6. THE bridge SHALL accept at most 10 messages per second per session, dropping and logging the excess.
7. THE system SHALL use no custom cryptography. Confidentiality and authentication come only from SSH (`golang.org/x/crypto/ssh` on the host, libssh on the device). Hashes are used only to detect changes, never for authentication.
8. Keys and fingerprints SHALL be compared and stored in full. Truncated fingerprints MAY appear only as display labels.
9. Bridge files SHALL live under `~/.config/herdr-pager/` with directory mode 0700 and secret files 0600.

### Requirement 14: Optional vibration motor (the portable pager, optional)

**User Story:** As a pager user on the go, I want a vibration motor like a real pager, so that I can be paged silently in a pocket.

#### Acceptance Criteria

1. WHEN provisioning includes `--vibe-gpio <n>` (header GPIOs only, validated against the list of free pins) THEN the device SHALL store the setting and drive a motor through a transistor on that pin.
2. WHERE a vibration motor is configured THE device SHALL pulse it with each page pattern, including in silent mode, and SHALL NOT pulse it during quiet hours.
3. WHERE no motor is configured THE device SHALL behave exactly as in Requirements 6 and 10.

### Requirement 15: Touch navigation

**User Story:** As a pager user, I want to flick through agents and read cards by touch, so that browsing is quick, while every decision stays on the physical buttons.

#### Acceptance Criteria

1. THE device SHALL read touch only while the backlight is on. Touch SHALL NEVER wake the screen, on any board revision (pocket safety).
2. Touch SHALL drive navigation only: select a row, open a card, scroll, go back, move between cards, and pick menu items. Touch SHALL NOT produce approve, deny, an interrupt YES, a factory-reset YES, a pairing confirmation or power off.
3. EVERY action reachable by touch SHALL also be reachable by the buttons alone.
4. ON the list, a tap on a row SHALL select it, a tap on the selected row SHALL open its card, a vertical swipe SHALL move the list by one screen, and a long press (≥ 1 s) on a row SHALL open that agent's menu.
5. ON a card, a horizontal swipe SHALL open the previous or next agent's card in list order, and a downward swipe SHALL return to the list. Opening a card by touch counts as opening it (7.4, 7.5).
6. Menus SHALL lay items out as horizontal tiles, each at least 120 × 96 px (about 16 × 13 mm). Any on-screen touch target other than a list row SHALL be at least 56 × 56 px (about 7.5 mm).
7. THE device SHALL ignore touch for 300 ms after the backlight turns on.
8. Scrolling SHALL move in discrete steps with no kinetic animation.

### Requirement 16: Networks and use on the go

**User Story:** As a pager user, I want the pager to work on my home Wi-Fi and on my phone's hotspot, so that I can carry it.

#### Acceptance Criteria

1. WHEN the user runs `pager-bridge provision` THEN it SHALL accept up to 4 Wi-Fi networks (`--wifi-ssid` repeated, in priority order) and SHALL read their passwords in the same order from no-echo prompts or from stdin lines (`--wifi-pass-stdin`).
2. WHILE not connected to Wi-Fi THE device SHALL scan at least every 60 seconds and join the visible saved network that is highest in the priority order.
3. WHEN a device is re-provisioned with the same public key THEN the tool SHALL update its existing `devices.toml` entry and SHALL NOT add a duplicate.
4. WHILE the device can't reach the bridge THE status bar SHALL show the link state and the Wi-Fi network in use (or "NO WIFI"). The device SHALL NOT page from stale data. WHILE the list is empty THE list's body SHALL say why: on a live link, "WAITING FOR HERDR" until the first snapshot, "HERDR OFFLINE" while herdr is down, and "NO AGENTS" only when herdr's list is empty (amended 2026-11-01 by the owner, open-items O-84); "NO WIFI"; "CONNECTING" and the bridge's address until a dial on the network in use fails; then "NO BRIDGE ON THIS NETWORK", asking whether the Mac is on that network. There, PWR SHALL show device info (amended 2026-10-27 by the owner, open-items O-77: on a phone's hotspot without the Mac, a pager that had restarted showed only the band over an empty body).
5. THE user docs SHALL state that the ESP32-S3 joins only 2.4 GHz networks. They SHALL give the phone settings: on iPhone 12 or later, turn on **Maximize Compatibility** and **Allow Others to Join**; on Android, set the hotspot band to 2.4 GHz.
6. THE user docs SHALL state which hotspot setups work, per the outcome of the hotspot spike (phase 3). The pager pages only while it shares a network with the host.

### Requirement 17: Off-LAN use through a tailnet — dropped

**Dropped 2026-10-18 (ADR 0005).** The pager doesn't join a tailnet; it reaches the bridge only on networks it shares with the host. The number is kept so traceability stays stable. With it went 17.4's prerequisite of flash encryption, NVS encryption and secure boot, which existed only to protect a stored tailnet credential.

### Requirement 18: Danger-aware cards

**User Story:** As a pager user, I want risky commands flagged loudly on the card, so that a glance tells me when to slow down.

#### Acceptance Criteria

1. WHEN the bridge builds a permission card THEN it SHALL extract the command or action the agent wants to run (the keymap's `command_match` pattern) and SHALL place it first among the card's detail lines. IF it can't read the command (or a file prompt's path, a fetch prompt's URL, which it reads only when its host is the one the prompt names, or an MCP call's server and tool and arguments, the tool within a line), the command, path, URL, tool or arguments hold an escape, a control character other than newline and tab, or a bidi or zero-width character, or the keymap's `approve_option` can't confirm that approve's keys pick the approve THEN the card SHALL fail closed: its first line SHALL be "COMMAND NOT READ" (on a file prompt, "FILE NOT READ"; on a fetch prompt, "URL NOT READ"; on an MCP call, "TOOL NOT READ"), followed by the command, path, URL or MCP call if one was read, and it SHALL offer deny and jump only (8.1, 8.6) (amended 2026-11-01 and 2026-11-02 by the owner, open-items O-80, O-88 and O-90).
2. THE bridge SHALL check the full, untruncated command against the danger rules (embedded `risk.toml` plus optional user overrides) before any truncation.
3. WHEN a rule matches THEN the card SHALL carry `risk:"danger"` and the rule's short label (for example "FORCE PUSH"). The visible text SHALL include the matched fragment even where head-and-tail truncation would otherwise hide it.
4. WHEN a danger card is displayed THEN the device SHALL show a full-width inverted "DANGER: <label>" banner and play the distinct `DANGER` tone pattern (unless silent). Approve SHALL need BOOT held for 2 seconds instead of 1.
5. The risk flag and label SHALL be included in the card hash (7.3).
6. THE user docs SHALL state that the rules are a best-effort reading aid, that an unflagged card is not a safe card, and that the pager is still not a security gate.
7. THE default rules SHALL cover at least:
   - recursive forced delete (`rm -rf`, `rm -fr`);
   - force push (`git push --force`, `-f`, `--force-with-lease`), `git reset --hard` and `git clean -fd`;
   - piping a download into a shell (`curl … | sh`, `wget … | bash`);
   - `sudo`, `chmod -R 777`, `dd … of=` and `mkfs`;
   - SQL `DROP` and `TRUNCATE`;
   - writes to `~/.ssh`, `~/.aws` or `.env` files;
   - reading or writing a file that holds a secret: the bridge's own settings and host key (`~/.config/herdr-pager`), the keychains, `~/.gnupg`, `.netrc`, Claude Code's credentials, kube, docker and gh configs, git credentials, npm and PyPI tokens, and private keys anywhere (SECRETS) (amended 2026-11-01, open-items O-88's review: a Read outside the folder can be approved from the pager);
   - a fetch of a local address: localhost, loopback, the link-local range with the cloud metadata address, and the private ranges (LOCAL ADDRESS) (amended likewise: a fetch can be approved from the pager).

   Each default rule SHALL have at least one matching and one non-matching test case.

### Requirement 19: Runs in the lcd349-kit emulator

**User Story:** As a developer, I want to build and use the whole pager without the board, so that UI work and agent-driven development don't wait on hardware.

#### Acceptance Criteria

1. THE firmware SHALL reach the hardware only through the lcd349-kit board SDK (a separate project, `~/lcd349-kit`), so the same sources build for the board (`esp32s3`) and for the kit's emulator (ESP-IDF `linux` target).
2. WHEN the pager runs in the emulator THEN it SHALL pair over the emulator's virtual serial port and connect to a real `pager-bridge` over the host network, exactly as the device does. Only `net/ssh_link` differs: it links the host's libssh instead of the board's `david-cermak/libssh` component (the same libssh API; phase 0's spike ran the same client code on both).
3. EVERY hardware checkpoint in tasks group 9 that doesn't depend on physical properties (loudness, battery, pocket handling, touch feel) SHALL have a scripted emulator equivalent that runs headless in CI.
4. Golden screenshots of every screen (list, permission card, danger card, question card, menu tiles, confirm dialogs, toasts, error and provisioning screens) SHALL be committed. CI SHALL fail on a pixel difference unless the goldens are updated in the same change.
5. EVERY screen SHALL pass the kit's UI lint (target sizes, contrast, clipping, glyph coverage) with no errors.

### Requirement 20: A typed approve call upstream in herdr (parallel track)

**User Story:** As the maintainer, I want herdr to offer a request-bound respond call, so that approvals stop depending on keystrokes.

#### Acceptance Criteria

1. THE project SHALL propose a request-bound respond method to herdr (discussion #4462): a short design note, then a pull request if the maintainers agree.
2. WHERE the running herdr's schema (`herdr api schema --json`) offers that method THE bridge SHALL use it for approve and deny instead of keystrokes, and SHALL still refuse stale verdicts (8.3, 8.4).
3. WHERE it doesn't THE bridge SHALL behave exactly as in Requirement 8.
4. Adding that one method to the herdr allowlist (13.2) SHALL be the only change to the allowlist.

### Requirement 21: Published metrics

**User Story:** As the maintainer, I want measured numbers for speed, noise and battery, so that the README's claims are evidence, not adjectives.

#### Acceptance Criteria

1. THE bridge SHALL record a timeline for every blocked episode, containing no prompt text, agent names or keys:
   - blocked observed;
   - snapshot sent;
   - card shown (`show`, which carries the milliseconds since the page fired);
   - verdict received;
   - keys sent;
   - how the episode ended: pager verdict, answered at the keyboard, or stale.
2. `pager-bridge stats [--since <duration>] [--json]` SHALL report:
   - episodes;
   - median and p95 time from page to verdict;
   - the share answered from the pager versus at the keyboard;
   - pages that cleared by themselves within 30 seconds (a noise measure);
   - the stale-verdict rate;
   - danger cards shown.
3. THE timeline file SHALL live under `~/.config/herdr-pager/` with mode 0600, rotate at 10 MB, and be deleted by `pager-bridge stats --reset`.
4. AT each published version THE README SHALL publish:
   - the N1, N2, N3, N7 and N12 results, and N9 and N11 once the portable pager has measured them;
   - once `stats` exists (the portable pager), a `stats` summary from at least 5 working days of real use;
   - the threat-model summary;
   - a demo video of 60 seconds or less.
   (Amended 2026-10-30, grilling items 25 and 26: v1.0 is the desk pager, published before the portable pager, ADR 0008.)

### Requirement 22: One-click install

**User Story:** As a newcomer, I want to install the bridge with one command and do the rest from one page in my browser, so that I'm paging in minutes rather than an afternoon.

#### Acceptance Criteria

1. EACH published version SHALL ship its firmware images with the bridge, and `pager-bridge setup` SHALL serve the setup page on the Mac, from which the pager is flashed from a Chromium-based browser over USB with no toolchain (ADR 0009).
2. EACH published version SHALL publish `pager-bridge` for macOS on Apple Silicon (arm64) with SHA-256 checksums, and a Homebrew tap formula that installs it with the firmware images. Linux and Intel Macs are not supported.
3. Pairing SHALL still happen only through the bridge over USB with on-device confirmation (Requirement 2), from `pager-bridge provision` or at the setup page's request. No page served from anywhere else SHALL provision Wi-Fi or keys.
4. THE setup page SHALL take a newcomer through flashing, the Wi-Fi networks, pairing, the firewall, run at login and the first page, showing the live output of each step, and SHALL manage the setup afterwards (devices, versions and updates, the bridge's checks).
5. THE setup page SHALL listen only on 127.0.0.1 behind a one-time token, SHALL run only while `pager-bridge setup` runs, and SHALL NOT approve, deny or send keys to an agent.
6. A Wi-Fi password entered on the setup page SHALL reach only the pager: never a file, a log, the page's output or a process's arguments.

(Amended 2026-10-30, grilling items 25 and 26: the setup page replaces the static flasher page; macOS on Apple Silicon only.)

### Requirement 23: Keymaps as tested data

**User Story:** As a contributor, I want to add support for a new coding agent by adding data files, so that support for Codex, Gemini and others needs no Go code and can't break silently.

#### Acceptance Criteria

1. EACH agent kind's keymap SHALL be a file `bridge/keymaps/<kind>.toml`. Its fixtures SHALL live in `bridge/testdata/herdr/<kind>/`: at least one permission prompt and one question prompt, each captured three times (at 0 s, 2 s and 70 s), and the agent list in each state. Prompts that ask the owner to choose rather than give leave (for Claude Code: plan approval and folder trust) are question prompts, and their fixtures are kept with the others (phase 0 R3, P-02 grilling 2026-10-20).
2. A test SHALL discover every keymap file and fail if any of these hold:
   - its fixtures are missing;
   - `permission_match` doesn't match every permission fixture, or matches any question fixture;
   - `command_match` extracts nothing from a permission fixture for a command;
   - any two captures of the same prompt hash differently.
3. EACH keymap SHALL record the agent version its fixtures were captured from and the capture date.
4. `CONTRIBUTING.md` SHALL turn the recon script (`bridge/recon/capture-claude.sh`, phase 0 R3) into a step-by-step guide for re-capturing an agent after a release and for adding an agent.

## Non-Functional Requirements

| ID | Requirement | Budget | How it is measured |
|---|---|---|---|
| N1 | Page latency | herdr status → `blocked` to device tone ≤ 6 s p95 (3 s debounce + ≤ 3 s pipeline) on the home LAN | Bridge integration test with the fake herdr: blocked → snapshot sent ≤ 1.5 s. On hardware: compare the device serial log timestamp (clock synced per 4.6) with the bridge log over 20 trials. |
| N2 | Verdict latency | Button press → `agent.send_keys` accepted ≤ 1.5 s p95 on LAN | Device log (press) vs bridge log (send_keys ok), 20 trials. Bridge-only path ≤ 200 ms in the integration test. |
| N3 | Recovery | Device reconnected ≤ 35 s after the bridge restarts. Snapshot resumes ≤ 2 s after herdr returns. | Kill and restart `serve` and stop/start herdr while watching the device serial log (5 trials each). |
| N4 | Bridge footprint | RSS ≤ 40 MB. Average CPU ≤ 1 % of one core with 10 agents at 1 Hz polling. | `ps -o rss=,%cpu=` sampled every 10 s for 5 minutes. |
| N5 | Device idle draw | ≤ 25 mA average at 5 V USB, screen off, connected, battery disconnected | USB-C power meter, 10-minute average. This budget is provisional: task 0.3 measures the board's baseline. If the baseline alone exceeds 20 mA, reset the budget to baseline + 5 mA and record the reason in design.md. Waiting for the USB meter (X-02, 2026-10-20). |
| N6 | Device stability | 24 h connected with snapshot churn every 10 s: no reboot; free heap at end within 10 % of the value at hour 1 | Serial log of `esp_get_free_heap_size()` every 10 min; bridge soak-mode fixture. |
| N7 | Boot to connected | ≤ 15 s from power-on to the first snapshot on known Wi-Fi, including a boot whose join the access point refuses up to twice (the owner's decision, 2026-10-27, open-items O-45: a refused join is tried again at once) | Device serial log timestamps, 5 trials. |
| N8 | Bridge test coverage | Every acceptance criterion in R1, R3, R4, R7.3, R8.3–8.7, R11–R13 has an automated Go test | `go test ./...` passes, and the traceability table in tasks.md lists each test. |
| N9 | Battery life on the go | ≥ 12 h from full on the fitted cell, connected to a phone hotspot, with 6 pages an hour and 30 s of screen per page | Run-down test from full charge with the device serial log timestamping each battery reading. Provisional: if the cell capacity recorded in task 0.1 divided by the measured average current is under 12 h, the budget becomes that figure and the reason is recorded in design.md. |
| N10 | ~~Battery life on a tailnet~~ | Dropped 2026-10-18 with requirement 17 (ADR 0005) | — |
| N11 | Time to decision | Page tone → verdict received by the bridge: median ≤ 5 s with the pager on the desk, ≤ 12 s from a pocket | `pager-bridge stats` over 20 trials of each (checkpoint 9.2 and 9.7). |
| N12 | Time to first page | A newcomer goes from unboxing to a first page in ≤ 15 minutes using only the README's install section, the Homebrew tap and the setup page (amended 2026-10-30) | Timed once by someone who hasn't seen the project, from `brew install`; if no one is available, by the owner on a fresh macOS user account, recorded as a proxy; recorded in `docs/measurements.md`. |

## Scope

**Re-scoped 2026-10-18 (`docs/product/mvp-scope.md`, ADRs 0001–0002):** the MVP is now the desk pager (`.kiro/specs/phase-2-desk-pager/`). "MVP (this spec)" below is split: the portable pager (`.kiro/specs/phase-3-portable-pager/`) and the optional publish phase (`.kiro/specs/phase-5-pager-publish/`, whose first release is v1.0). The acceptance criteria themselves are unchanged; the phase specs cite them as "parent N.M".

### MVP (this spec)

Requirements 1–13, 15, 16, 18, 19, 21, 22 and 23, and N1–N9, N11 and N12: pairing, SSH link, monitoring, list, paging, cards, hold-to-approve and deny with stale-check, menu (snooze, jump, interrupt, settings), power management with pocket safety, touch navigation, multiple Wi-Fi networks including a phone hotspot, danger-aware cards, the emulator build with golden screenshots, published metrics, one-click install, keymaps as tested data, bridge operations, device management, and the security constraints. Claude Code is the only agent kind with a keymap in the MVP.

**Dependency:** the MVP needs lcd349-kit v0.1 (its board SDK and emulator). It shipped as the kit's local tag `v0.1.0` on 2026-10-23 (kit phase 1), and the desk pager builds against it.

### Parallel track (not part of any phase's definition of done)

- Requirement 20: a typed approve call in herdr. It depends on herdr's maintainers.

### Phase 1.5: on the go anywhere — dropped

Dropped 2026-10-18 (ADR 0005): requirement 17 (tailnet), N10, and the encryption and secure boot that 17.4 required. Requirement 14 (vibration motor) moved to the portable pager as an optional add-on.

### Phase 2

- IMU gestures: raise-to-wake, and face-down to snooze the current page.
- Codex and other agent keymaps, each verified like Claude Code's in task 0.4.

### Roadmap (explicitly deferred — do NOT build)

- OpenSSH forced-command mode on Linux hosts (`pager-bridge stdio` already exists).
- `events.subscribe` instead of polling.
- Free-text replies.
- Multiple hosts per pager.
- OTA updates.
- A custom enclosure or extra buttons.
- A real approval gate in the agent's own permission layer (e.g. Claude Code's channel permission relay).

### Cut list (drop in this order if the build runs into trouble)

1. Quiet hours (6.6 quiet-hours clause, 9.6 quiet-hours item). Silent mode remains.
2. Touch input (Requirement 15 and the 9.2 touch clause). Buttons alone drive everything (15.3 guarantees it).
3. Snooze (9.3). Acknowledging a page via its card still stops reminders.
4. Done tone (6.2). Done agents still sort to the top of the list.
5. Battery display and low-battery page (10.5).
6. Homebrew tap (22.2 tap clause). Release binaries and checksums remain.
7. The setup page's flashing (22.1). The docs fall back to `idf.py flash`.
8. `stats` reporting (21.2). The timeline is still recorded.

Never cut: hold-to-approve (8.1), the wake-only press (10.7) and touch never waking the screen (15.1). They are what make the pager safe in a pocket.

**Abort threshold:** the build has failed if it can't deliver all of these: full-key SSH in both directions (R3), monitoring (R4), the list (R5), the blocked page (6.1), cards (R7), approve/deny with stale-check (R8), and the security constraints (R13).

## Assumptions and RECON

Recon performed 2026-10-11 on the host (macOS 26.6.2, arm64), and updated 2026-10-12 and 2026-10-20 (P-03: the board facts the kit measured at bring-up):

- **VERIFIED — herdr:** herdr 0.9.3 at `~/.local/bin/herdr`. The server is running and the socket is `~/.config/herdr/herdr.sock` (mode `srw-------`). `herdr api schema --json` reports protocol 22. The schema confirms:
  - the request envelope `{id, method, params}`, with responses `{id, result}` or `{id, error}`;
  - `agent.list` returns `{type:"agent_list", agents:[AgentInfo]}`;
  - `AgentInfo` has `pane_id`, `agent`, `display_agent`, `agent_status` (idle|working|blocked|done|unknown), `state_change_seq`, `completion_seq`, `name`, `title`, `revision`;
  - `agent.read` takes `{target, source: visible|recent|recent_unwrapped|detection, format: text|ansi, strip_ansi, lines}` and returns `{text, revision, truncated}`;
  - `agent.send_keys` takes `{target, keys:[string]}`, and the CLI help names `esc` as the canonical Escape key.
- **VERIFIED — Go:** go1.26.4 darwin/arm64.
- **VERIFIED — Remote Login is off:** port 22 is closed and `/etc/ssh/ssh_host_ed25519_key.pub` is absent. This drives design D1 (the bridge embeds its own SSH server).
- **VERIFIED — no ESP toolchain:** `idf.py`, `arduino-cli`, `pio` and `esptool` are absent. Task 0.1 installs ESP-IDF.
- **VERIFIED — device not attached:** no `/dev/cu.usbmodem*` device.
- **VERIFIED — board facts** (Waveshare wiki and demo repo `waveshareteam/ESP32-S3-Touch-LCD-3.49`; the demo repo has no stated licence, so reference it but don't copy it):
  - BOOT = GPIO0 and PWR = GPIO16, both active-low;
  - power hold = TCA9554 P6;
  - backlight = GPIO8;
  - touch on I2C 0x3B (SCL 18 / SDA 17), polled: no usable interrupt on either revision (the P-03 entries below);
  - battery on ADC1 channel 3 with a ×3 divider;
  - RTC 0x51, IMU 0x6B;
  - LCD on QSPI (CS 9, PCLK 10, D0–D3 11–14, RST 21).
- **VERIFIED — touch screen (2026-10-12; updated 2026-10-20):** Waveshare's wiki lists "Touch Type: Capacitive touch" over I2C, built into the AXS15231B display driver chip. The design uses single-touch gestures (tap, long press, swipe) and no pinch. The controller in fact reports two points; the first is used (P-03, below). Physical geometry: the 2026-10-12 estimate from the 3.49" diagonal was about 86 × 23 mm (7.5 px per mm); Waveshare's drawing gives 84.00 × 22.58 mm (7.62 px per mm), pending the ruler (X-03, below).
- **VERIFIED — battery (2026-10-12):** Waveshare's wiki lists "Case B (with 3.7V Lithium polymer battery)", and the user confirms the case has one. The capacity isn't published; task 0.1 reads it from the cell label and N9 depends on it.
- **RESOLVED — LED (2026-10-12):** not needed. The user had read "LCD" as "LED". No LED appears in any requirement.
- **VERIFIED — board revision and touch interrupt (2026-10-20, P-03):** the board is V2, inferred from TP_INT pulses (the silkscreen wasn't read, and needn't be). TP_INT reaches the TCA9554's P0, but the panel's TE signal on P5 makes the expander's interrupt line (GPIO42) fire about 122 times a second, so there's no usable touch interrupt. Touch is polled every 30 ms on any revision: the controller reports about every 16.8 ms, and reads faster than 20 ms return "no new report" frames that look like lifts. Touch never wakes the screen (15.1). Source: `lcd349-kit/spikes/hw/RESULTS.md` and `device/device.json`.
- **VERIFIED — touch through the case, and the button positions (2026-10-20, P-03):** touch works through Case B's front and a screen protector. The controller reports two points, each with an ID; the kit and the pager use the first. Touches land 6–26 px below where the finger aims (x is off by 1–4 px). In landscape (cw90, the buttons edge on top), both usable buttons sit at the right-hand (USB-C) end of the top edge, 10 mm apart, BOOT rightmost: PWR about 69 mm and BOOT about 80 mm from the active area's left edge. Where their hints go is decided in grilling item 16 (P-15). Source: `lcd349-kit/spikes/hw/RESULTS.md` and `device/device.json`.
- **RECON NEEDED (phone hotspot):** the ESP32-S3 is 2.4 GHz only. Apple says iPhone 12 and later run the hotspot at 5 GHz unless Maximize Compatibility is on. Apple doesn't document whether hotspot clients can reach each other, and forum reports conflict. Task 0.5 tests it:
  - Pager and Mac on the same hotspot can reach each other on :2277 and mDNS resolves → same-hotspot use is supported in the MVP (16.6 docs say so).
  - Unicast works but mDNS fails, or neither works → same-hotspot use isn't supported; the pager works on home and office Wi-Fi. The docs say so, and no subnet probing is added. (There is no tailnet: ADR 0005.)
- **DROPPED — tailnet on the device (2026-10-18, ADR 0005).** MicroLink was never built here; the decision rests on its published facts (MIT; about 116 KB of static internal RAM; a 3 s network heartbeat; no releases).
- **VERIFIED — speaker (2026-10-20, P-03):** a speaker is connected, and its amplifier is enabled on TCA9554 P7, confirmed by sound. Never toggle P6. Source: `lcd349-kit/spikes/hw/RESULTS.md` and `device/device.json`.
- **VERIFIED — Claude Code keys and prompt text (2026-10-21, phase 0 P-02):** Claude Code 2.1.306 under herdr 0.9.3 (protocol 22). Source: `bridge/testdata/herdr/claude/KEYS.md`, `DETECTION.md` and the fixtures beside them; the keymap is `bridge/keymaps/claude.toml`.
  - **approve = `["1"]`:** `1` alone resolves the prompt, with no `enter`. A second request for the same command shows an identical prompt, so `1` is "Yes", not "don't ask again".
  - **deny = `["esc"]`:** denying the danger prompt (`git push --force`) left the bare repository's branch where it was. `esc` also denies file edit and file create prompts, and declines plan approval.
  - **interrupt = `["esc"]`:** the command stops, and Claude Code waits at its input box. Never `ctrl+c`, which can exit Claude Code.
  - All three match the assumption; none was flagged.
  - **The captured fixtures define `permission_match`.** It matches the shell, file edit, file create and danger prompts, and not the question or plan-approval prompts.
  - **For phase 2:**
    - while the agent's pane is unfocused, herdr reports every ended turn as `done` until it's seen, an interrupted one too: after `esc`, detection reads idle but `agent_status` is `done`;
    - focusing the pane turns done into idle (the jump clears done);
    - Claude Code runs `ls` in its working folder without asking, even in manual mode;
    - the folder-trust prompt wasn't captured at 2.1.306 (Claude Code remembered an earlier trust). At 2.1.305 its cursor started on "No, exit", with unnumbered options, and accepting took `down`, `enter`. Phase 2 treats it as unknown (jump only).
- **VERIFIED — SSH on the device (2026-10-21, phase 0 P-01):** `david-cermak/libssh` 0.12.2, a plain ESP-IDF component (LGPL-2.1 as a separate library), with an **ed25519** device key. **arduino-esp32 isn't needed.** Design D4 is updated. Source: `spikes/ssh_esp/RESULTS.md` and its logs in `spikes/ssh_esp/logs/`.
  - **N1 handshake (P0-N1):** 489–718 ms in each trial.
  - **Internal RAM for the link (P0-N2):** about 21 KB, peak 29.6 KB, against 64 KB, whether mbedTLS sits in internal RAM or PSRAM.
  - **The link held** 30 minutes with keepalives (drift −24 B), and 30 minutes at maximum modem power saving. A silent bridge was noticed in 89 s, a closed one in 23 ms.
  - The same client code passed against the Mac's Homebrew libssh (the emulator path).
- **VERIFIED — display (2026-10-20, P-03; pager task 0.3):** a full-frame flush takes 13 ms with strip rotation (the kit adopts it in phase 1), 29.7 ms with rendering, and 75.7 ms on the kit's current whole-frame path. All three are under 100 ms, so animation isn't restricted. The panel takes no row address, so every redraw is a full frame. It refreshes at 61 Hz (TE every 16.41 ms). Source: `lcd349-kit/spikes/hw/RESULTS.md` and `device/device.json`.
- **VERIFIED — memory after start-up (2026-10-20, P-03):** about 216 KB of internal RAM and 7.9 MB of PSRAM free after the bring-up spike's initialisation. The link's budget is ≤ 64 KB of internal RAM (phase 0 P0-N2). Source: the kit's `spikes/hw/logs/touch-period-sweep.log`.
- **Driver-level facts are the kit's** (2026-10-20, P-03): backlight polarity and boost enable, the expander-reset hazard, malformed touch frames, raw touch rotation and the I2C buses are in `lcd349-kit/spikes/hw/RESULTS.md` and `device/device.json`. The pager reaches the hardware only through the kit.
- **WAITING — screen size (X-03):** Waveshare's drawing gives an active area of 84.00 × 22.58 mm (7.62 px per mm, 194 PPI). The owner's ruler decides between that and 86 mm (phase 1's K-07).
- **VERIFIED — battery capacity (2026-10-21):** 2,500 mAh at 3.7 V (9.25 Wh), rated 35 A, as the owner read it from the cell (2026-10-21). Waveshare doesn't publish it. For N9's 12 hours, the pager's average draw must stay under about 208 mA (2,500 mAh ÷ 12 h, before conversion losses).
- **WAITING — idle current (N5):** needs a USB-C inline power meter (X-02). N5 stays provisional.
- **VERIFIED — agent kind string (2026-10-21, phase 0 P-02):** herdr reports `AgentInfo.agent` = `claude` for Claude Code, as expected (the name of herdr's `claude.toml` manifest). The keymap is keyed on that value. Source: `bridge/testdata/herdr/claude/VERSIONS`. herdr reports no token count for the agent (`tokens` and `usage` are null), and no `agent_session` or `state_labels` under clean settings.
- **VERIFIED — display driver licence (2026-10-12):** `espressif/esp_lcd_axs15231b` on the Espressif Component Registry is Apache-2.0 and drives both the AXS15231B panel (QSPI) and its touch controller. lcd349-kit uses it for the panel, so no Waveshare code is needed. The kit reads touch itself: the component's touch driver gets release, malformed frames and two points wrong (P-03, 2026-10-20). A community write-up of another AXS15231B board reports that the panel only works with full-frame LVGL refresh and has no hardware rotation; the kit's design assumes both.
- **RECON NEEDED (entropy at key generation):** the ESP32-S3 RNG is only truly random while Wi-Fi/BT is running or after `bootloader_random_enable()`. Task 6.2 verifies that key generation runs only after one of those holds.
