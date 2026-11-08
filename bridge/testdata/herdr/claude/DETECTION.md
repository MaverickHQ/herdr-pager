# How herdr classified the recon's captures

Claude Code 2.1.306, herdr 0.9.3 (protocol 22), captured 2026-10-21 by `bridge/recon/capture-claude.sh` (phase 0, task 4.8; requirement 3.12). This file only records what herdr said. Phase 2's grilling decides whether the bridge relies on herdr's own classification.

The isolated named session `herdr-pager-recon` ran headless. The agent's pane (`w1:p1`, tab `w1:t1`) stayed unfocused until the focus check: the script's plain shell pane (`w1:p3`, tab `w1:t2`) held focus throughout.

## Detection captures

State and rule are from `herdr agent explain --json` (`explain/<capture>.json`), and `state_labels` and `agent_session` from `herdr agent get` (`get/<capture>.json`). Printed by `capture-claude.sh summary`.

| capture | herdr state | rule | state_labels | agent_session present |
|---|---|---|---|---|
| `shell-0s`, `shell-2s`, `shell-70s` | blocked | `bash_permission_prompt` | absent | no |
| `shell-repeat` | blocked | `bash_permission_prompt` | absent | no |
| `long-prompt` | blocked | `bash_permission_prompt` | absent | no |
| `danger` | blocked | `bash_permission_prompt` | absent | no |
| `file-edit-0s`, `file-edit-2s`, `file-edit-70s` | blocked | `legacy_no_prompt_blocker` | absent | no |
| `file-create-0s`, `file-create-2s`, `file-create-70s` | blocked | `legacy_no_prompt_blocker` | absent | no |
| `question-0s`, `question-2s`, `question-70s` | blocked | `live_blocked_form` | absent | no |
| `plan-0s`, `plan-2s`, `plan-70s` | blocked | `legacy_no_prompt_blocker` | absent | no |
| `working-now` | working | `osc_title_working` | absent | no |
| `interrupted-now` | idle | `live_prompt_box` | absent | no |

- `shell-*` is `touch approved.txt` (4.4), `long-prompt` is `ping -c 100 127.0.0.1` (4.5), and `danger` is `git -C work push --force` (4.7).
- herdr names its rules in its explanations. The file edit, file create and plan-approval prompts all fall to the same fallback rule, `legacy_no_prompt_blocker`; only the Bash prompts have a rule of their own.
- **The 0 s, 2 s and 70 s captures of each prompt are identical, byte for byte.** So are the boxes of the first and the repeated shell prompt. Nothing in a blocked screen changed over 70 s (`capture-claude.sh diffs` prints nothing), which is why the keymap's `ignore_match` is empty.
- One deterministic redaction: herdr's explanation names its manifest as `remote:<home>/.local/state/herdr/agent-detection/remote/claude.toml`, so the script replaces the home folder with `~` in every `explain/*.json`.

## agent.list in each state (`list/`)

| file | agent_status | state_change_seq | focused |
|---|---|---|---|
| `idle.json` (at startup, before any task) | idle | 1 | false |
| `blocked-permission.json` (the `touch` prompt) | blocked | 3 | false |
| `done.json` (after the approved `touch` finished) | done | 5 | false |
| `working.json` (while `ping` ran) | working | 12 | false |
| `blocked-question.json` | blocked | 15 | false |
| `before-focus.json` (the focus check) | done | 29 | false |
| `after-focus.json` (the focus check) | idle | 29 | true |

Each list holds only the recon's own agent. `AgentInfo.agent` is `claude`.

## `agent_session` and `state_labels`

- `agent_session` is **absent** from every `agent get` and `agent list`. herdr's hook doesn't load under the clean settings (`--safe-mode --setting-sources project`).
- `state_labels` is absent too, and so are `tokens` and `usage`: herdr has no token count for the recon's agent.

## Done, and the focus check (requirement 3.14)

- With the shell pane focused, **every ended turn showed as done**: the approved `touch` (`list/done.json`), the answered question, the denied edit, create and push, the declined plan, and the interrupted `ping` (`get/interrupted-now.json`: `agent_status` done, while herdr's detection reads idle). The first run, whose agent pane was the session's only and focused pane, went from working straight to idle.
- **The focus check passed.** After the last task (the denied push), the agent showed done with its pane unfocused (`list/before-focus.json`). `herdr agent focus recon` then turned it idle within the 15 s wait, with `focused: true` (`list/after-focus.json`). The `state_change_seq` stayed 29: herdr doesn't count done to idle as a state change. That's the proof that the pager's jump clears done.

## Not captured

not captured: trust

- **The folder-trust prompt** (best effort, R3.17). Claude Code 2.1.306 didn't show it: it remembered that the first run trusted `/private/tmp/herdr-pager-recon`. The recon doesn't change the owner's Claude Code settings to make it ask again. The first run captured it at 2.1.305 (herdr: `blocked` by `live_blocked_form`). Those fixtures were removed, because every fixture must come from one version (R3.2). Phase 2 treats the trust prompt as unknown, so its card offers only jump. Its keys are in `KEYS.md`.

## The tool prompts (open item O-88)

Claude Code 2.1.316, herdr 0.9.3 (protocol 22), captured 2026-11-01 by `capture-claude.sh` (`tool-versions`, then `webfetch`, `read-outside`, `mcp` and `webfetch-long` in one session, with the script's clean settings), and the MCP prompt on 2026-11-02 in a session of its own (§The MCP prompt). They're a set of their own, `tool-prompts/`, with its own `VERSIONS`, since every capture of a set comes from one version (R3.2) and phase 0's set stays at 2.1.306. Each prompt was captured, then denied with `esc`; the recon never approves one.

| capture | herdr state | rule | state_labels | agent_session present |
|---|---|---|---|---|
| `tool-prompts/webfetch-0s`, `-2s`, `-70s` | blocked | `legacy_no_prompt_blocker` | absent | no |
| `tool-prompts/webfetch-long` | blocked | `legacy_no_prompt_blocker` | absent | no |
| `tool-prompts/read-outside-0s`, `-2s`, `-70s` | blocked | `bash_permission_prompt` | absent | no |
| `tool-prompts/mcp-0s`, `-2s`, `-70s` | blocked | `bash_permission_prompt` | absent | no |
| `tool-prompts/mcp-long` | blocked | `bash_permission_prompt` | absent | no |
| `tool-prompts/mcp-desc` | blocked | `bash_permission_prompt` | absent | no |
| `tool-prompts/mcp-safe-mode-unexpected` | idle | `live_prompt_box` | absent | no |

- **WebFetch** (`webfetch-*`, `https://example.com/`): the box's title line is `Fetch`, then `Claude wants to fetch content from example.com` (the URL's host, as Claude Code parsed it), a `╌` rule, `url: https://example.com/`, `prompt: ` and the question the agent asks of the page, a second `╌` rule, then `Do you want to allow Claude to fetch this content?` over `1. Yes`, `2. Yes, and don't ask again for example.com` and `3. No, and tell Claude what to do differently (esc)`. There's no `Esc to cancel` line. Phase 0's `permission_match` didn't match its question, so the bridge took it for a question.
- **A URL longer than a line** (`webfetch-long`): the URL wraps, and each of its lines starts with a `│` gutter (` │ url: https://…`, ` │ -the-quick-brown-fox`).
- **Read outside the folder** (`read-outside-*`, `/etc/shells`): the title line is `Read file`, then a `╌` rule, `Read(/etc/shells)`, a second `╌` rule, `Do you want to proceed?` over `1. Yes`, `2. Yes, allow reading from /etc and /private/etc during this session` and `3. No`, and `Esc to cancel · Tab to amend`. herdr reads it with its Bash rule. Phase 0's `file_prompt` matches its title, but `file_match` read no path from it (its second line is the rule), so it said FILE NOT READ.
- **An MCP tool call** (`mcp-*`, the recon's stub's `echo` with `text` "hello from the recon"): the title line is `Tool use`, then `recon — Echo Tool: (MCP)` (the server, an em dash, the tool's name as Claude Code shows it, and `(MCP)`), a `╌` rule, one line per argument, `text: "hello from the recon"` (the value as JSON), a second `╌` rule, then `About the recon — Echo Tool:` over the tool's description in a `│` gutter (`│ Returns the text it is given.`), and `Do you want to proceed?` over `1. Yes`, `2. Yes, and don't ask again for recon — Echo commands in /private/tmp/herdr-pager-recon` and `3. No`, and `Esc to cancel · Tab to amend`. herdr reads it with its Bash rule. Phase 0's keymap took it for a Bash prompt it couldn't read: COMMAND NOT READ, deny only.
- **An argument longer than a line** (`mcp-long`): the argument's line is only its name, `text:`, and its value follows unquoted, in a `│` gutter, wrapped.
- **The boxes of the 0 s, 2 s and 70 s captures of each prompt are identical.** Above the Read and MCP prompts' boxes, their tool line changes (`⏺ Reading 1 file…`, `⏺ Calling recon…`, with and without the `⏺`); the bridge never reads above the box, so `capture-claude.sh check` now compares the boxes only, and `ignore_match` stays empty.

### The MCP prompt

- **Safe mode loads no MCP server** (2026-11-01, best effort, as R3.17). With the script's clean settings, Claude Code 2.1.316 loads no MCP server, not even the one `--mcp-config` names: `--safe-mode` disables MCP (its banner says so, `⚠ Safe mode: all customizations are disabled (CLAUDE.md, skills, plugins, hooks, MCP, agents, and more)`). The recon's stub server (`bridge/recon/mcpstub`) was built and named in `--mcp-config`, beside `--strict-mcp-config`, but no stub process ever ran, and asked to call its `echo` tool, the agent answered that it couldn't find one (`tool-prompts/detection/mcp-safe-mode-unexpected.txt`). Capturing the prompt needs a session without `--safe-mode`, and that one wouldn't be clean: `--setting-sources project` keeps the owner's settings out (and with them hooks and allow rules), but not necessarily the rest of what safe mode disables (CLAUDE.md, skills, agents). That was the owner's decision, not the recon's (O-90, below); until the capture, an MCP tool call's card was a permission card that couldn't read its command (COMMAND NOT READ, deny only).
- **The isolated session can't sign in** (2026-11-02, O-90, the owner's decision: no `--safe-mode`, but a config folder of its own). `start-isolated` started Claude Code 2.1.316 with every clean flag but `--safe-mode`, and `CLAUDE_CONFIG_DIR` an empty 0700 folder in the state folder (the process check: 1 nested Claude Code, 0 with `--safe-mode`, 1 with that folder, 1 with `DISABLE_AUTOUPDATER=1`). `onboard` passed its first screen, `Choose the text style that looks best with your terminal`, with `enter`. The next was `Welcome to Claude Code v2.1.316` and `Select login method:` over `1. Claude account with subscription · Pro, Max, Team, or Enterprise`, `2. Anthropic Console account · API usage billing` and `3. 3rd-party platform · Amazon Bedrock, Microsoft Foundry, Google Vertex AI`. A fresh config folder holds no sign-in, and the owner's rule is never to copy, link or read one. `onboard` stopped there, choosing nothing; nothing was captured, no message was sent, and `cleanup` removed the session and the folder (33 s in all).
- **With the recon's own sign-in it was captured** (2026-11-02, the owner's decision: `~/.herdr-pager-recon/claude`, signed in once by the owner by hand, 0700; the recon only names it, never reads it). The process check: 1 nested Claude Code, 0 with `--safe-mode`, 1 with that folder, 1 with `DISABLE_AUTOUPDATER=1`; the stub server ran. `onboard` passed `Choose the text style that looks best with your terminal` with `enter`, then a screen ending `Press Enter to continue…` with `enter` (none of its words asked to choose a model or a plan, or to accept terms or a policy), and accepted the folder-trust dialog for `/private/tmp/herdr-pager-recon` (`down`, `enter`). The input box's screen then showed the banner (the model and the account's plan), the tip `Get to finished work sooner with Opus 5.5. Switch anytime with /model.` and `Fast mode is now available · /fast to turn on`; `onboard` first stopped at the tip's `/model` (agent `done`, not `idle`), and, fixed to take the input box in manual mode as ready whatever the tip, found it ready. The first `mcp` saved its prompt as unexpected, since its box names `Echo`, not `echo`; that copy was dropped, the box pattern fixed, and `mcp` and `mcp-long` captured it. Each was denied with `esc`, and the stub logged no call. 3 messages.
- **A description over several lines: not captured** (2026-11-02, O-90's review). The stub gained a second tool, `note`, whose description has blank lines and markdown, and `mcp-desc` was to capture its prompt once. In the same sign-in folder, Claude Code 2.1.316 opened with a screen it hadn't shown before: `Claude in Chrome extension detected`, `Claude will use your Chrome browser by default — navigating sites, filling forms, and capturing screenshots in your existing session.`, `Browser actions still go through Claude's regular permission prompts before they run. Turn browser tools off for future sessions with /chrome.`, over `❯ No, keep browser tools off` and `Yes, use my browser`, and `Enter to confirm · Esc to keep browser tools off`. `onboard` stops at any browser screen, so it stopped there, choosing nothing; no message was sent, nothing was captured, and `cleanup` removed the session. It was captured later (below).
- **Again, after the Chrome screen: still not captured** (2026-11-02, the owner's decision: `onboard` answers that screen with `esc`, its own "keep browser tools off", and nothing else). `onboard` sent `esc` to the Chrome extension screen, and the next screen no longer showed it: the input box, with the banner, the `/model` tip, `1 more notice hidden`, and its placeholder after a no-break space (`❯` U+00A0 `Try "how does <filepath> work?"`), which the ready check now allows. `mcp-desc` then sent its one message, and Claude Code answered with its own usage limit, not a tool call: its usage-limit message. The recon's check found no prompt, saved the screen as unexpected and sent `esc`, which cancelled the automatic continue (`Automatic continue cancelled · /rate-limit-options to re-arm`); that capture was dropped (it held the reset time and time zone), and `cleanup` removed the session. The description was captured once the limit had reset (below).
- **The description over several lines, captured** (`mcp-desc`, 2026-11-02, once the limit had reset). `onboard` found the input box ready at once (no Chrome or other first-run screen this time). The box: `Tool use`, `recon — Note Tool: (MCP)`, a rule, `text: "hello from the recon"`, a rule, `About the recon — Note Tool:`, then only the description's first two lines, each in the `│` gutter, its blank lines dropped and its markdown left as it is (`│ Keeps a note.`, `│ Use it for **short** notes:…`, cut with `…`), then `(ctrl+o to expand description)` without a gutter, and `Do you want to proceed?` over a plain `1. Yes`. The rest of the description never reaches the screen. Denied with `esc`; the stub logged no call; 1 message. Its card: `recon - Note Tool`, `text: "hello from the recon"`, approve, deny and jump: one question line, so `question_match` lets approve through; a test makes the unguttered `(ctrl+o …)` line a question and `1. Yes` with the real option 1 changed, and it fails closed.

## From the first run (Claude Code 2.1.305), recorded as text

- In safe mode and manual mode, Claude Code ran `ls` in its working folder **without a permission prompt**: it treats a listing as read-only. That's why the approve check now uses `touch approved.txt`, which writes and so asks. Its evidence fixtures are gone, because they came from 2.1.305.
- That run's nested Claude Code also auto-updated the owner's Claude Code to 2.1.306. This run started the nested session with `DISABLE_AUTOUPDATER=1`: `ps` showed it in the nested `claude` process's environment, and the version stayed 2.1.306.
