# Cards match the raw prompt; sanitizing is for display

The bridge decides what a prompt is from its raw detection text, and sanitizes only what it shows, last. It cuts the prompt box out of herdr's detection text (the lines below the keymap's `box_start`, the last rule above the prompt). It then matches `permission_match` against that raw box, extracts the command or file path from it, and runs the danger rules on that raw command or path. Only then does it sanitize each field a card or snapshot carries. The parent design had it the other way round, sanitizing first. That breaks matching: the sanitizer turns box drawing into ASCII, so phase 0's `command_match` (which needs `╌`) would never match. It also weakens the danger rules, which would see a rewritten command. Matching over the whole screen, as before, lets an old "Do you want to proceed?" in the scrollback turn a question into a permission card, which a verdict would then answer with the approve key.

**Status:** accepted, 2026-10-22 (phase 2's grilling, item 13).

## Consequences

- Nothing matched or flagged ever depends on what the sanitizer does, and nothing the pager shows has skipped it. The sanitizer's guarantee (printable ASCII within a limit) stays as strong as before.
- Keymaps gain `box_start`, `drop_lines` and `file_match`, all data (parent requirement 23). A new agent kind is still a keymap and its fixtures, not code.
- The danger rules see what the agent will really run. Rules must cope with real command lines, such as git's global options before the subcommand.
- The rest of the screen (old conversation, the agent's launch line) never reaches a card.
- A sanitizer that kept box drawing was rejected: it would make the sanitizer's guarantee depend on which characters matching needs.
