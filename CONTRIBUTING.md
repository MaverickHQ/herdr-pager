# Contributing

## Issues are welcome

Open an issue for a bug, a question, a card the pager got wrong, a prompt it can't read, a danger rule that should exist, or an agent you'd like it to support. It helps to include:

- what you did, what you expected and what happened;
- `pager-bridge --version` and `pager-bridge doctor`;
- the pager's firmware version, from its device info (the `FW` line);
- your macOS version, `herdr --version` and `claude --version`;
- the bridge's log around the problem: `$(brew --prefix)/var/log/herdr-pager.log` under `brew services`, `~/Library/Logs/herdr-pager/bridge.log` under the launchd agent, or `serve`'s stderr. At its default level it never holds card text, agent names or passwords, but it does hold pane ids, pager names and your bridge's address.

Never paste a Wi-Fi password, the bridge's host key (`~/.config/herdr-pager/host_ed25519`) or a whole `devices.toml`. For a card that came out wrong, describe the prompt, or paste the agent's prompt box with anything private taken out.

For a security problem, don't open an issue: [`SECURITY.md`](SECURITY.md) says how to report it privately.

## Pull requests aren't taken yet

This repository is published from a private working copy, one commit per version (`docs/adr/0010`), so a pull request here can't be merged. Please open an issue describing the change instead, and attach what it needs: a keymap, its captures, a danger rule and its tests. That may change once the project settles.

## The keymap guide

A **keymap** tells the bridge how to read one kind of agent's permission prompts and which keys answer them: `bridge/keymaps/<kind>.toml`. It's data, not code: adding an agent, or catching up with a new version of one, needs no Go. Every keymap is tested against **captures**: real screens of the agent's prompts, saved by a recon script that drives a throwaway session of the agent through herdr.

Claude Code's keymap is `bridge/keymaps/claude.toml`, its captures are in `bridge/testdata/herdr/claude/`, and its recon is `bridge/recon/capture-claude.sh`.

### What the recon does, and doesn't, do to your Mac

- It runs herdr under its own named session, `herdr-pager-recon`, headless and with herdr's own variables dropped, so it never touches your live herdr or its panes. `cleanup` stops and deletes that session.
- It works in a throwaway folder, `/tmp/herdr-pager-recon`, with its own state in `/tmp/herdr-pager-recon-state`.
- It starts Claude Code on Haiku with clean settings (`--safe-mode --setting-sources project --strict-mcp-config --permission-mode manual`), so none of your settings, hooks, MCP servers or allow rules load, and with `DISABLE_AUTOUPDATER=1`, so it can't update your Claude Code. It uses your Claude Code sign-in, and so your plan's usage.
- It sends at most 25 messages, and stops 30 minutes after Claude Code starts. `start <messages-used>` carries the count over a run that stopped part-way.
- It never approves a risky prompt. Each prompt is captured, then denied with `esc`. The only things it approves are a `touch approved.txt` and a `ping -c 100 127.0.0.1` in the throwaway folder, to learn the approve key, and only when the prompt shows exactly that command; and the folder-trust prompt for that folder.
- It passes herdr only its own fixed text and herdr's ids, never text the agent wrote.
- Before it saves a capture, it checks it for private data (your home folder's path, login and full name, host name, git email, and your Claude account's email and name) and refuses to save one that holds any. It names which kind it found, never the value.

You need herdr on your PATH, Claude Code signed in, `jq` and bash.

### Re-capture Claude Code after a release

Run each step from the repository's root, one at a time, and read what it prints before the next.

```sh
bridge/recon/capture-claude.sh claude-flags   # this Claude Code still has the clean-settings flags
bridge/recon/capture-claude.sh versions       # clears the old captures; records herdr's version and protocol, and Claude Code's
bridge/recon/capture-claude.sh server-up      # the recon's own headless herdr session
bridge/recon/capture-claude.sh start          # the throwaway folder, the agent's pane, a focused shell pane, Claude Code
bridge/recon/capture-claude.sh trust          # the folder-trust prompt; or `idle`, if Claude Code shows none
bridge/recon/capture-claude.sh shell          # a Bash prompt x3, approved; then done
bridge/recon/capture-claude.sh shell-again    # the same prompt again: approve picked "Yes", not "don't ask again"
bridge/recon/capture-claude.sh working        # a long command, approved; working; interrupted with esc
bridge/recon/capture-claude.sh question       # a two-option question x3, answered
bridge/recon/capture-claude.sh file-edit      # the edit prompt x3, denied
bridge/recon/capture-claude.sh file-create    # the create prompt x3, denied
bridge/recon/capture-claude.sh plan           # the plan-approval prompt x3, declined
bridge/recon/capture-claude.sh danger         # git push --force to a throwaway repository, denied
bridge/recon/capture-claude.sh focus-check    # done, then jump to the pane: done becomes idle
bridge/recon/capture-claude.sh tool-versions  # clears the tool prompts' captures; records their versions
bridge/recon/capture-claude.sh webfetch       # a web fetch x3, denied
bridge/recon/capture-claude.sh webfetch-long  # a URL longer than a line, denied
bridge/recon/capture-claude.sh read-outside   # a Read outside the folder x3, denied
bridge/recon/capture-claude.sh cleanup        # stops and deletes the session and the recon's folders
```

If a step stops with an unexpected screen, it saves it as evidence and says so. `screen` shows the agent's status and the last lines on its screen; `cleanup` always leaves your Mac as it was.

**The MCP prompts** need one more session: safe mode loads no MCP server at all, not even the recon's own stub. So `start-isolated` starts Claude Code without `--safe-mode`, every other clean flag kept, in a config folder of the recon's own, so none of your own files load (CLAUDE.md, skills, agents, settings, plugins). Sign in there once, by hand:

```sh
mkdir -p ~/.herdr-pager-recon/claude && chmod 700 ~/.herdr-pager-recon ~/.herdr-pager-recon/claude
CLAUDE_CONFIG_DIR=~/.herdr-pager-recon/claude claude     # sign in, then /exit
```

Then:

```sh
bridge/recon/capture-claude.sh server-up
bridge/recon/capture-claude.sh start-isolated
bridge/recon/capture-claude.sh onboard        # passes the first-run screens; stops at any sign-in, plan, terms or browser screen
bridge/recon/capture-claude.sh mcp            # the stub server's echo tool x3, denied
bridge/recon/capture-claude.sh mcp-long       # an argument longer than a line, denied
bridge/recon/capture-claude.sh mcp-desc       # a tool whose description runs over several lines, denied
bridge/recon/capture-claude.sh cleanup
```

The recon only names that folder; it never reads a file in it.

**Then check what changed:**

```sh
bridge/recon/capture-claude.sh check          # offline: every capture there, the JSON parses, no private data, one version per set, the keymap's patterns against the boxes
bridge/recon/capture-claude.sh diffs          # lines that change between captures of one prompt: should print nothing
bridge/recon/capture-claude.sh summary        # herdr's state and rule for each capture, for DETECTION.md
git diff --stat bridge/testdata/herdr/
cd bridge && go test ./internal/keymap/ ./keymaps/ ./internal/card/ && go test ./...
```

1. **Read the diff of the captures.** What matters is the prompt box: its title line, the rules (`╌`) around the command, path, URL or arguments, the question, and the option list. A change above the box doesn't matter; the bridge never reads it.
2. **Update `bridge/keymaps/claude.toml`:** `agent_version` and `captured`, and any pattern the new screens broke. Keep every pattern anchored to the box and failing closed: when a pattern can't be sure, it reads nothing, and the card offers only deny. Approve is offered only when `approve_option` confirms that the key approve sends picks a plain `1. Yes`, and exactly one line asks the question (`question_match`).
3. **Update the records beside the captures:** `manifest.toml` (what each capture is, and the command, path, URL or call the keymap must read from it), `DETECTION.md` (what herdr said of each, from `summary`) and `KEYS.md` (if the keys changed, and how you saw it).
4. **The bridge's tests decide.** `TestKeymapDiscovery` loads every keymap and checks it against its captures, through the manifest: `permission_match` matches every permission capture and no question capture, each head reads as the manifest says, and the three captures of one prompt read the same box, so their cards hash the same. The keymap's own tests (`bridge/keymaps/claude_test.go`) check the rest.
5. If the cards look different, the emulator's golden screens will say so: `lcd349 build -C firmware && cd bridge && PAGER_EMUTEST=1 go test -timeout 30m ./emutest/...`.

### Add an agent

1. **Find its kind:** the `agent` string herdr reports for it in `agent.list` (Claude Code's is `claude`). The keymap is `bridge/keymaps/<kind>.toml`, holding one table, `[<kind>]`, and its captures go in `bridge/testdata/herdr/<kind>/`.
2. **Make its recon:** copy `capture-claude.sh` to `bridge/recon/capture-<kind>.sh`, and change how `start` launches the agent (its own clean-settings flags: no user settings, hooks, MCP servers or auto-update), the prompts it asks the agent to send, and the screens it waits for. Keep every safety rule above.
3. **Capture, at the least:**
   - one permission prompt and one question prompt, each three times, at 0 s, 2 s and 70 s, so the tests can show nothing on a blocked screen changes;
   - the agent list in each state: idle, working, blocked on a permission, blocked on a question, done;
   - a prompt that asks you to choose rather than to give leave (a plan, a folder's trust) is a question prompt, and is kept with the others;
   - `VERSIONS`, with the agent's version, and `KEYS.md`: which keys approve, deny and interrupt, and how you saw each one work.
4. **Describe the captures** in `manifest.toml`, in the format `bridge/testdata/herdr/claude/manifest.toml`'s header gives.
5. **Write the keymap.** `claude.toml`'s comments explain each field, and [`docs/bridge.md`](docs/bridge.md#keymaps-and-danger-rules) how the bridge uses them:
   - `agent_version` and `captured`, the date;
   - `approve`, `deny` and `interrupt`: keys from `esc`, `enter`, `tab`, the arrows, `backspace`, `0`–`9`, `a`–`z` and `ctrl+<letter>`. Never a key that can quit the agent (for Claude Code, `interrupt` is `esc`, never `ctrl+c`);
   - `box_start` and `drop_lines`: where the prompt box starts, and its lines that never reach a card;
   - `permission_match`, `command_match`, `approve_option` and `question_match`, and if the agent has them, `file_prompt` and `file_match`, `url_prompt` and `url_match`, `mcp_prompt` and `mcp_match`;
   - `ignore_match`, for lines that change while the agent waits (none, for Claude Code).

   A kind with no `permission_match` gets no approve or deny, and one with no `approve_option` no approve: its cards offer deny and jump at most.
6. **Try it before it's built in:** `~/.config/herdr-pager/keymaps.toml` takes the same tables, and may add a kind. Restart the bridge to load it. A key or pattern it doesn't accept stops `serve` with an error naming the file and the key.
7. **Run the tests:** `cd bridge && go test ./internal/keymap/ ./keymaps/ && go test ./...`. `TestKeymapDiscovery` finds the new keymap by itself.
8. **Send it** in an issue: the keymap, the recon's changes and the captures, after `check`'s privacy scan has passed on them.

## Licence

herdr-pager is Apache-2.0 (`LICENSE`); the third-party software it uses, and their licences, are in `THIRD_PARTY.md`.
