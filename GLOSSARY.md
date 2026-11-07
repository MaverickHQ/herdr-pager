# herdr-pager

A physical pager that tells its owner which herdr coding agents need them, and lets them approve or deny from the device. This glossary also names the projects and milestones around it.

## Projects

**herdr-pager**:
The product: the pager device and the bridge program that connects it to herdr.
_Avoid_: the v1's name, the pager project

**lcd349-kit**:
The separate platform herdr-pager is built on: the board's SDK, its emulator and the tools agents use to build for it.
_Avoid_: the BSP, the emulator (when the whole kit is meant)

**v1**:
The archived first idea, voice messages over SSH. Its name survives only as the private working copy's folder name (ADR 0003).
_Avoid_: its name, for herdr-pager

## The link

**Bridge**:
The program on the owner's Mac that is the only thing talking to herdr, and that the pager connects to.
_Avoid_: server, daemon, host app

**Link**:
The authenticated connection the pager always opens to the bridge.
_Avoid_: session, tunnel, connection

**Device key**:
The pager's own key pair, made on the device and never leaving it.
_Avoid_: client key, pager certificate

**Host key**:
The bridge's key, which the pager pins at pairing and checks on every link.
_Avoid_: server key

**Fingerprint**:
A short form of a key, shown so the owner can compare keys by eye. It never authenticates anything.
_Avoid_: key ID

**Pairing**:
The one-time exchange over USB in which the bridge learns the pager's device key and the pager learns the bridge's host key, confirmed by the owner on the pager and then on the setup page or the terminal, before any Wi-Fi password is sent.
_Avoid_: enrolment, registration

**Protocol message**:
One line the pager and the bridge send each other over the link.
_Avoid_: message (on its own: that's what the owner types to an agent), packet, command

## People

**Owner**:
The one person a pager belongs to, who uses it and decides everything about it.
_Avoid_: user, operator

## Agents

**Agent**:
A coding agent, such as Claude Code or Codex, running in a herdr pane.
_Avoid_: session, bot

**Agent kind**:
Which program an agent is, as herdr names it (Claude Code is `claude`). It decides which keymap applies.
_Avoid_: agent type, model

**Message**:
What the owner types to an agent.
_Avoid_: prompt

**Working**:
An agent busy on its task.
_Avoid_: running, busy

**Blocked**:
An agent stopped at a permission or question prompt, waiting for the owner.
_Avoid_: waiting, stuck, needs input

**Done**:
An agent that has finished, which the owner hasn't looked at yet.
_Avoid_: finished, complete

**Idle**:
An agent waiting for a message, with nothing new for the owner.
_Avoid_: ready, stopped

**Permission prompt**:
What a blocked agent shows when it asks leave to use a tool: to run a command, to edit or create a file, to read one outside its folder, to fetch a URL, or to call an MCP tool.
_Avoid_: prompt (on its own), dialog, request

**Question prompt**:
What a blocked agent shows when it asks the owner to choose or answer, including plan approvals and folder trust. It never takes a verdict.
_Avoid_: prompt (on its own)

**Keymap**:
For one agent kind, the keys that carry each verdict and an interrupt to the agent, and the patterns that recognise its prompts.
_Avoid_: bindings, key config

## The pager

**Card**:
The pager's screen for one agent. For a blocked agent, its prompt, sanitised, with the command, file, URL or MCP tool first; for any other agent, that nothing needs you, and what it's doing.
_Avoid_: popup, detail view

**Verdict**:
The owner's approve or deny on a permission prompt's card.
_Avoid_: answer, response, decision

**Snapshot**:
The list of agents and their states that the bridge sends the pager whenever one of them changes.
_Avoid_: update, refresh, screenshot

**Page**:
The pager's alert that an agent has become blocked: a tone, flashes and the screen lit, once for each prompt, with reminders until the owner looks.
_Avoid_: alarm, notification, beep

**Done tone**:
The pager's short tone, played once when an agent finishes a turn, with the screen lit and its row selected. It has no flashes or reminders, and the owner can turn it off. It isn't a page.
_Avoid_: done page, completion alert

**Seen**:
Said of a done agent once the owner has opened it or jumped to it: it stops counting as new.
_Avoid_: read, acknowledged (that's a page the owner has looked at)

**Stale**:
Said of a verdict whose card no longer matches the agent's current prompt, because its state or its text has changed since the card was shown. A stale verdict is never carried to the agent.
_Avoid_: expired, outdated

**Pairing mode**:
The state in which a pager waits to be paired over USB: on its first start, after a factory reset, or when the owner chooses Pair again.
_Avoid_: provisioning mode, setup mode

**Button hint**:
The label on the screen next to a button, saying what that button does on the current screen.
_Avoid_: soft key, legend, hint bar

**Fill bar**:
The bar in the BOOT button hint that fills while BOOT is held on a permission card, showing how long until the approve is sent.
_Avoid_: progress bar, hold meter

**Card guard**:
The half second after a card appears in which a new press is ignored, so a press meant for the screen before can't act on the card.
_Avoid_: debounce, lockout

**Wake-only press**:
The press that turns a dark screen on, and does nothing else.
_Avoid_: wake press, first press

**App core**:
The part of the pager's firmware that decides everything it does: given what happened and when, it says what the pager should show, sound and send. It never touches the hardware.
_Avoid_: business logic, controller, state machine

**Render model**:
What the app core says the screen should show, before anything is drawn.
_Avoid_: view model, UI state

## Milestones

**Day in use**:
A day on which the owner works with agents in herdr and the pager pages or takes verdicts. Seven of them, not necessarily in a row, stand between one milestone and building the next.
_Avoid_: trial day, test day

**Desk pager**:
herdr-pager's first useful form: on the owner's desk, on USB power and home Wi-Fi, paging when an agent is blocked and taking approve or deny verdicts. It is the initiative's MVP.
_Avoid_: MVP, pager MVP

**Kit MVP**:
lcd349-kit working on the real board, tagged locally for herdr-pager to build on, and not published.
_Avoid_: MVP, kit v0.1, internal v0.1

**Portable pager**:
herdr-pager off the desk, for the owner's own use: on battery and a phone hotspot, navigable by touch, with snooze, quiet hours and its own usage statistics. Never published as such.
_Avoid_: v1.0, MVP

**v1.0**:
The first published version of herdr-pager: the desk pager, made for a newcomer to set up alone.
_Avoid_: MVP, the full pager, the first release

## Publishing

**Published version**:
A numbered state of herdr-pager or lcd349-kit that anyone can download: its source, and for herdr-pager the firmware and the bridge, ready to install.
_Avoid_: release (on its own: that's the kit's touch decision), drop, build

**Newcomer**:
Someone setting herdr-pager up from a published version, without having seen the project. Once their pager is paired, they are its owner.
_Avoid_: user, customer, new user

**Setup page**:
The page on the owner's Mac, served by the bridge, that takes a newcomer from a blank pager to a first page (flashing, Wi-Fi, pairing, running the bridge) and afterwards manages that setup, showing the live output of each step. It never takes a verdict.
_Avoid_: web flasher (flashing is only its first step), dashboard, web UI, control panel

## Delivery

**Baseline**:
A repository's first commit, holding its files as they stood before any phase.
_Avoid_: initial commit, snapshot

**Slice**:
One behaviour taken from a failing test to a passing one at one agreed seam: the unit of work in a phase's task list.
_Avoid_: story, ticket, step

**Phase gate**:
The full automated test run that must pass before a phase's work is committed.
_Avoid_: gate (on its own), CI

**Use gate**:
The 7 days in use that must pass before the next milestone's work starts.
_Avoid_: gate (on its own), trial period

**Checkpoint**:
A step in a phase that needs the owner: to look, listen, touch, carry or measure.
_Avoid_: human test, manual step, gate

**Handoff**:
The note left when unattended work stops for the owner, saying why and what to do next.
_Avoid_: alert, ticket

**Foundation**:
The first part of a phase, done before any lane starts, that lays down everything the lanes share: dependencies, file layout, interfaces and the fixtures both sides test against.
_Avoid_: setup, scaffolding, bootstrap

**Lane**:
A chain of a phase's task groups that one agent works through on its own branch and worktree, alongside other lanes, changing only the paths it owns.
_Avoid_: track, stream, worker, branch (for the work itself)

**Merge queue**:
The step that takes finished lanes one at a time, reviews each, merges it into the phase branch and checks the result before taking the next.
_Avoid_: merge train, integration (on its own)

**Merge stage**:
The end of a phase, after every lane has merged: the records only it may write, the whole-phase review, checkpoints, approvals, the phase gate and the commit.
_Avoid_: wrap-up, finalisation

**Board lock**:
The one claim on the board that every flash and board run takes, because there is one board; whoever holds it may flash, and everyone else waits.
_Avoid_: board mutex, reservation

**Board test**:
A test that runs on the board itself and reports its result over USB, so an agent can check hardware behaviour without the owner.
_Avoid_: hardware test, HIL (that's phase 6's hardware in the loop)

**Board check**:
One of the pager's own checks on the board: a test on the Mac that flashes the pager's firmware, drives it over USB, and checks or times what it does.
_Avoid_: board test (that's the kit's, which runs on the board itself)

**Soak**:
A long unattended run of a product in the emulator that checks it neither crashes nor keeps growing in memory.
_Avoid_: burn-in, endurance test

**Test build**:
Pager firmware built for checks on the board: it takes scripted button presses over USB and logs timings. It is never the firmware the owner uses.
_Avoid_: debug build

**Release build**:
Pager firmware built for use, which ignores scripted input.
_Avoid_: release (on its own: that's the kit's touch decision), production build

**Golden**:
A saved picture of a screen that the screen must match, apart from the parts marked as changing, such as the clock.
_Avoid_: snapshot (that's the agent list the bridge sends), reference image

**Fake herdr**:
A stand-in for herdr that plays recorded agents and their prompts and notes every request, so the bridge is tested without ever touching the owner's herdr.
_Avoid_: mock herdr, stub

**Devsim**:
A stand-in for the pager in the bridge's tests: a program that opens a link and speaks the protocol.
_Avoid_: fake pager, device mock

## The board

**Parity**:
The emulator showing what the board shows, checked side by side: the same layout, colours and text, and a modelled flush time close to the measured one.
_Avoid_: fidelity (the list of what the emulator models at all), sync

**Release**:
The kit's decision that a finger has left the screen: the controller reports a lift, or stays silent for three polls in a row.
_Avoid_: lift (the controller's event, one of the two causes), touch up

**False release**:
A release while the finger is still down, which a product would see as a lift and often a click.
_Avoid_: phantom lift, dropout

