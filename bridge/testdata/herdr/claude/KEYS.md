# Claude Code's keys, as observed by the recon

Claude Code 2.1.306, herdr 0.9.3 (protocol 22), captured 2026-10-21 by `bridge/recon/capture-claude.sh` (phase 0, task 4.8; requirement 3.13). Every key was sent with `herdr agent send-keys` to the recon's own agent, in the isolated session `herdr-pager-recon`, with the script's shell pane focused.

| Verb | Keys | Pager design's assumption | Same? |
|---|---|---|---|
| approve | `1` | `1` | yes |
| deny | `esc` | `esc` | yes |
| interrupt | `esc` | `esc` | yes |

All three pass the pager design's key check (`^(esc|enter|tab|up|down|left|right|backspace|[0-9]|[a-z]|ctrl\+[a-z])$`). None is `ctrl+c`.

## approve: `1` (task 4.4)

- The prompt for `touch approved.txt` (`detection/shell-*.txt`) lists `1. Yes`, `2. Yes, and always allow access to /private/tmp/herdr-pager-recon from this project`, `3. Yes, and switch to auto mode …` and `4. No`, with the cursor on `1. Yes`.
- `1` alone resolved it: two seconds later the agent had left that prompt (a new `state_change_seq`), so no `enter` was needed. Then `approved.txt` existed, and the agent finished the turn (`done`).
- Asked for the same command again, Claude Code showed a second prompt whose box was identical to the first (`detection/shell-repeat.txt`). So `1` picked "Yes", not "don't ask again". Approved again with `1`.
- `1` also approved the long-running command in 4.5 (`detection/long-prompt.txt`, `ping -c 100 127.0.0.1`), whose option 2 is `Yes, and don’t ask again for: ping *`.

## deny: `esc` (task 4.7)

- The `git -C work push --force` prompt (`detection/danger.txt`) says `Esc to cancel`; its option 2 is `Yes, and don’t ask again for: git *`.
- After `esc`, the throwaway bare repository's `main` was where it had been (the clone held an amended, unpushed commit, so a push would have moved it). The agent finished the turn (`done`).
- `esc` also denied the file edit prompt (`notes.txt` stayed `hello`) and the file create prompt (`created.txt` was never created), and declined the plan-approval prompt (`plan.txt` was never created).

## The tool prompts: `esc` denies (open item O-88)

Claude Code 2.1.316, 2026-11-01 and, for the MCP prompt, 2026-11-02 (`tool-prompts/`, DETECTION.md §The tool prompts).

- `esc` denied the MCP prompt (`tool-prompts/detection/mcp-*.txt`, and the long argument's and the long description's), which says `Esc to cancel`: the tool line then said `Interrupted · What should Claude do instead?`, the agent finished the turn (`done`), and the stub server logged no call. Its option 1 is a plain `1. Yes`, the cursor on it; option 2 is `Yes, and don't ask again for recon — Echo commands in …`. approve was never pressed on it.

- `esc` denied the WebFetch prompt (`tool-prompts/detection/webfetch-*.txt`, and the long URL's), whose option 3 says `No, and tell Claude what to do differently (esc)`, and the Read prompt for `/etc/shells` (`read-outside-*.txt`), which says `Esc to cancel`. Each time the tool line then said `Interrupted · What should Claude do instead?`, and the agent finished the turn (`done`) with no result from either tool (`tool-prompts/detection/mcp-safe-mode-unexpected.txt` shows both lines).
- **approve was never pressed on them:** the recon never approves a tool prompt. On both, option 1 is a plain `1. Yes`, the cursor on it, as on the Bash prompts, where `1` alone resolved the prompt; their option 2 is the "don't ask again" (`Yes, and don't ask again for example.com`; `Yes, allow reading from /etc and /private/etc during this session`).

## interrupt: `esc` (task 4.5)

- Sent while `ping -c 100 127.0.0.1` ran (`list/working.json`: `working`).
- Claude Code stopped the command and showed `Interrupted · What should Claude do instead?` above its empty input box (`detection/interrupted-now.txt`).
- herdr's own detection then read **idle** (`live_prompt_box`), but `agent_status` was **done** (`get/interrupted-now.json`, a new `completion_seq`). With the shell pane focused, herdr reports every ended turn as done until the agent's pane is seen, and an interrupted turn too. Focusing the pane turns done into idle (the focus check in `DETECTION.md`).

## The folder-trust prompt: `down`, `enter` (task 4.3)

- **Not shown in this run.** Claude Code 2.1.306 started straight at its input box: it remembered that the first run (2.1.305) trusted `/private/tmp/herdr-pager-recon`. The recon doesn't change the owner's Claude Code settings to make it ask again.
- What the first run saw, at 2.1.305 (recorded as text; its fixtures are gone): the cursor starts on `No, exit`, and the options have no numbers. `down` then `enter` accepted it. `1` or `enter` alone would have picked `No, exit`.

## Other keys the recon used (not keymap keys)

- **The question prompt:** `1` picked `1. Red` and submitted the answer at once (no `enter`).
- **Plan mode:** `shift+tab` (herdr accepts that name) cycles Claude Code's modes. Two presses went from `manual mode on` to `plan mode on`, and two more came back. `shift+tab` fails the pager's key check, so the bridge can never send it.
