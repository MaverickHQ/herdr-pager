# The danger rules

When an agent asks to do something risky, its card says so: a full-width **DANGER: *label*** banner, a tone of its own, and a 2 s hold of BOOT to approve instead of 1 s. The danger rules are what decide.

**They're a reading aid, not a gate.** They catch the common risky commands written plainly, and miss obfuscated ones and tools they don't know. A card with no banner isn't a safe card: read its first line every time. And the pager itself is an attention device, not a security gate: any process in a herdr pane can already drive herdr's socket (README §[Safety](../../README.md#safety)).

## What they read

Each rule is a regular expression (Go's RE2) matched against the first line of a permission card, raw, before anything is cleaned or cut to fit the screen:

| On a card for | The rules read |
|---|---|
| A Bash command | The whole command |
| A file edit, a new file, or a read outside the folder | The file's path |
| A web fetch | The URL |
| An MCP tool call | Its arguments |

Each rule is tried on the text as it is, and again on a copy with its quote characters (`"` and `'`) and its `\n`, `\t`, `\"` and `\'` escapes turned into spaces, so a rule fires inside quotes too: `bash -c "sudo …"`, an MCP argument's quoted value. Rules are tried in order, and the first that matches gives the banner its label.

The screen holds 40 characters a line. When a long command is cut to fit, the part a rule matched stays on screen, between the start and the end of the command, so you always see what set the banner off.

## The rules that come with it

| Label | Flags |
|---|---|
| RM -RF | A recursive, forced `rm`, however its options are written (`-rf`, `-fr`, `-r -f`, `--recursive --force`) |
| FORCE PUSH | `git push --force`, `-f`, `--force-with-lease`, or a `+` refspec, with or without git's global options (`git -C dir`, `-c k=v`) |
| RESET HARD | `git reset --hard` |
| GIT CLEAN | `git clean -f`, `-fd`, `-fdx`, `--force` |
| PIPE TO SHELL | `curl` or `wget` piped into a shell or an interpreter (`sh`, `bash`, `python`, `node` …), with or without `sudo`; and `bash <(curl …)`, `sh -c "$(curl …)"` |
| CHMOD 777 | A recursive `chmod 777` |
| DD | `dd` writing to `of=` |
| MKFS | `mkfs`, `newfs` and their variants |
| ERASE DISK | `diskutil eraseDisk`, `eraseVolume`, `zeroDisk`, `secureErase`, `partitionDisk`, `apfs deleteContainer` and the like |
| SQL DROP | `DROP TABLE`, `DATABASE`, `SCHEMA`, `VIEW`, `INDEX`, `USER` or `ROLE` |
| SQL TRUNCATE | SQL's `TRUNCATE` (not the `truncate` command) |
| SSH FILES | Anything under `.ssh`: keys, `authorized_keys`, `config` |
| AWS CREDENTIALS | Anything under `.aws` |
| ENV FILE | `.env` and `.env.<anything>` files |
| SECRETS | Files that hold secrets, read or written: the bridge's own settings and host key (`~/.config/herdr-pager`), the macOS keychains, GnuPG, `.netrc`, Claude Code's credentials and `~/.claude.json`, kube, docker and gh configs, git credentials, npm and PyPI tokens, and private keys anywhere (`id_rsa` and its kind but not their `.pub`, `*.pem`, `*.key`) |
| LOCAL ADDRESS | A fetch of a local address: `localhost`, loopback, `0.0.0.0`, the private ranges, the link-local range with the cloud metadata address (`169.254.169.254`, `metadata.google.internal`). Only a URL the card starts with, so a command that merely names one (`curl localhost:3000` in a dev loop) isn't flagged |
| SUDO | `sudo`, anywhere in a command. It's last, so a more specific rule names the danger first |

The rules are in [`bridge/internal/risk/risk.toml`](../../bridge/internal/risk/risk.toml), each with the commands it must flag and the ones it mustn't.

## Your own rules

Put rules of your own in `~/.config/herdr-pager/risk.toml`, in the same format:

```toml
[[rule]]
label          = "TERRAFORM DESTROY"
pattern        = '''\bterraform(\s+-[^\s;&|]+)*\s+(destroy|apply(\s+[^\s;&|]+)*?\s+-destroy)(\s|$)'''
tests_match    = ["terraform destroy", "terraform -chdir=infra destroy -auto-approve", "terraform apply -destroy"]
tests_no_match = ["terraform plan", "terraform apply", "terraform plan -destroy", "terraform-docs destroy.md"]
```

| Field | What |
|---|---|
| `label` | The banner's text after `DANGER:`. 1 to 32 printable ASCII characters, one rule per label |
| `pattern` | A Go (RE2) regular expression. Write it in `'''…'''`, so its backslashes stay as they are |
| `tests_match` | At least one text the pattern must flag |
| `tests_no_match` | At least one text it must not |

- **Your rules come after the ones that come with it,** in the order you write them. A rule with the same label as one of those replaces it, in its place.
- **Every rule's own tests run when the bridge starts.** Each test is matched against the text exactly as you wrote it (not the copy with its quotes as spaces), so write tests in the form your pattern reads. If a rule's tests fail, its pattern doesn't compile, a field is missing or unknown, or two of your rules share a label, `pager-bridge serve` stops with an error naming the file and the line. A bad rule never falls back silently to none.
- **Restart the bridge after a change:** `brew services restart herdr-pager`, or stop and start `pager-bridge serve`. `pager-bridge doctor` and its log say whether it's running.
- **A token of a command** is easiest matched as `[^\s;&|]+`, so a rule never reaches past a `;`, `&` or `|` into the next command. The rules that come with it are written that way.

## What a banner changes

- The card shows DANGER: *label* across its top, and plays the danger tone as it opens (unless the pager is silent).
- Approve takes BOOT held for **2 s**, not 1, with the bar filling under BOOT.
- The danger flag and its label are part of the card's hash: if the prompt changes before the keys go out, nothing is sent and the pager shows SCREEN CHANGED, as for any card.

A card the bridge couldn't read (COMMAND NOT READ, FILE NOT READ, URL NOT READ or TOOL NOT READ) offers only deny, banner or not: approve that one at the keyboard.
