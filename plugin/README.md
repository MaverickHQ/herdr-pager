# herdr-pager for Claude Code

A Claude Code plugin with one skill, `herdr-pager`, for setting up and checking [herdr-pager](https://github.com/MaverickHQ/herdr-pager), the desk pager for the coding agents in herdr. Ask Claude Code to set up your pager, to check why it won't connect, or what a message on its screen means.

The skill does four things:

- checks the install: macOS on Apple Silicon, and `pager-bridge` from Homebrew or a source build;
- runs `pager-bridge doctor --json`, explains each check that fails, and fixes what's safe with your yes;
- opens the setup page (`pager-bridge setup`) for whatever needs you: flashing, Wi-Fi, pairing, the firewall, run at login;
- explains the pager's screens, codes and buttons.

It never asks for a Wi-Fi password, and never pairs or revokes a pager itself. A password goes only into the setup page, in your browser, and from there to the pager. There's no MCP server: the skill runs `pager-bridge`'s own commands.

## Install

```
/plugin marketplace add MaverickHQ/herdr-pager
/plugin install herdr-pager@herdr-pager
```

It needs `pager-bridge` v1.0 or later (`brew install maverickhq/tap/herdr-pager`).
