#!/usr/bin/env bash
# capture-claude.sh: the herdr and Claude Code recon (phase 0, P-02, requirement 3).
#
# Drives one throwaway Claude Code session through herdr's own commands and saves
# what the bridge will see: agent.list in each state, detection reads of each
# prompt, and herdr's explanation of each. Re-run it after a Claude Code release
# and review the diff of bridge/testdata/herdr/.
#
# Safety rules this script keeps (requirement 3):
#   - herdr_call is the only place herdr runs. It always targets the named
#     session herdr-pager-recon, with a clean environment (herdr's own variables
#     dropped), so it never reaches the owner's live server or panes.
#   - Arguments to herdr are this script's own fixed text and herdr's ids only,
#     never text an agent produced.
#   - Fixtures are checked for private data before they are saved.
#   - The tool prompts (MCP, WebFetch, Read outside the folder; O-88) are
#     captured, then denied with esc: never approved.
#
# Subcommands: check [dir] | selftest | help-topic <topic> | ... (see usage)

set -euo pipefail

SESSION="herdr-pager-recon"
RECON_DIR="/tmp/herdr-pager-recon"
# The script's own state (pane id, start time, message count), kept outside the
# recon folder so the nested agent never sees it in `ls`.
STATE_DIR="/tmp/herdr-pager-recon-state"
AGENT="recon"
MAX_MESSAGES=25
MAX_SECONDS=1800
# The recon's own stub MCP server (bridge/recon/mcpstub, standard library
# only, stdio, one tool: echo), built by start into the state folder, and the
# file it logs each tool call to (open item O-88).
MCP_STUB="$STATE_DIR/mcpstub"
MCP_CONFIG="$STATE_DIR/mcp.json"
MCP_LOG="$STATE_DIR/mcp-calls.log"
# R3.5: Haiku, with clean settings, so none of the owner's settings, hooks, MCP
# servers or allow rules load. --mcp-config names the recon's own stub, the
# only MCP server --strict-mcp-config allows, for the MCP prompt (O-88); but
# --safe-mode loads no MCP server at all, so in Claude Code 2.1.316 the stub
# never runs (DETECTION.md §The tool prompts).
CLAUDE_ARGS=(--model claude-haiku-5-5 --safe-mode --setting-sources project
             --strict-mcp-config --mcp-config "$MCP_CONFIG" --permission-mode manual)
# The MCP prompt's own session (O-90, the owner's decision, 2026-11-02): since
# safe mode loads no MCP server, `start-isolated` starts Claude Code without
# --safe-mode, every other flag kept, and with a config folder of its own
# instead, so none of the owner's user files load: CLAUDE.md, skills, agents,
# settings, plugins. An empty folder stops at Claude Code's sign-in (the run of
# 2026-11-02), so the folder is the recon's own sign-in (the owner's decision,
# 2026-11-02): ~/.herdr-pager-recon/claude, outside the repository, made 0700
# and signed in once by the owner by hand, then reused and never removed.
# Only `onboard`, `mcp` and `mcp-long` run in it; the other captures refuse it.
ISOLATED_CONFIG="$HOME/.herdr-pager-recon/claude"
ISOLATED_CONFIG_SHOWN="~/.herdr-pager-recon/claude" # for messages: no home path in the output
CLAUDE_ARGS_ISOLATED=()
isolated_args() {
  local a
  for a in "${CLAUDE_ARGS[@]}"; do [[ "$a" == --safe-mode ]] || CLAUDE_ARGS_ISOLATED+=("$a"); done
}
isolated_args
# Every pane the script opens gets this environment (R3.5):
#   - a neutral shell prompt (the default shows the login name and hostname;
#     macOS's /etc/bashrc overwrites PS1, so PROMPT_COMMAND sets it per prompt);
#   - no shell history, so the panes stay out of the owner's history;
#   - DISABLE_AUTOUPDATER=1, inherited by the nested Claude Code, so it can't
#     update the owner's Claude Code (the first run's did).
PANE_ENV=(--env "PROMPT_COMMAND=PS1='\$ '" --env "HISTFILE=/dev/null" --env "DISABLE_AUTOUPDATER=1")
# The isolated session's agent pane gets its own config folder too (O-90).
PANE_ENV_ISOLATED=("${PANE_ENV[@]}" --env "CLAUDE_CONFIG_DIR=$ISOLATED_CONFIG")

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
TESTDATA="$REPO_ROOT/bridge/testdata/herdr"
CLAUDE_DIR="$TESTDATA/claude"
# The tool prompts (open item O-88): an MCP tool call, WebFetch, and Read
# outside the folder, captured after phase 0's set, at a later Claude Code,
# into a set of their own with its own VERSIONS (every set comes from one
# version, R3.2).
TOOL_DIR="$CLAUDE_DIR/tool-prompts"
# Where capture_stem saves: phase 0's set, unless a tool prompt's
# subcommand points it at TOOL_DIR.
CAPTURE_DIR="$CLAUDE_DIR"
KEYMAP="$REPO_ROOT/bridge/keymaps/claude.toml"

die() { printf 'capture-claude: %s\n' "$*" >&2; exit 1; }
log() { printf 'capture-claude: %s\n' "$*" >&2; }

# ---------------------------------------------------------------------------
# The one function that runs herdr.
# ---------------------------------------------------------------------------

herdr_bin() {
  local b
  b="$(command -v herdr || true)"
  [[ -n "$b" ]] || die "herdr not found on PATH"
  printf '%s' "$b"
}

# herdr_call <args...>: every herdr invocation goes through here.
# - env -i keeps only HOME, PATH, USER, LOGNAME, TERM and LANG, so HERDR_ENV,
#   HERDR_SOCKET_PATH, HERDR_PANE_ID and the rest never leak in from the pane
#   this script runs in.
# - --session pins every call to the recon's own named session.
#   herdr_call --detach <args...> runs it in the background (the headless server).
herdr_call() {
  local bin
  bin="$(herdr_bin)"
  if [[ "${1:-}" == "--detach" ]]; then
    shift
    nohup env -i \
      HOME="$HOME" PATH="$PATH" USER="${USER:-}" LOGNAME="${LOGNAME:-}" \
      TERM="${TERM:-xterm-256color}" LANG="${LANG:-en_US.UTF-8}" \
      "$bin" --session "$SESSION" "$@" </dev/null >/dev/null 2>&1 &
    disown || true
    return 0
  fi
  env -i \
    HOME="$HOME" PATH="$PATH" USER="${USER:-}" LOGNAME="${LOGNAME:-}" \
    TERM="${TERM:-xterm-256color}" LANG="${LANG:-en_US.UTF-8}" \
    "$bin" --session "$SESSION" "$@"
}

# assert_isolated: the server herdr_call reaches is the recon's own named
# session, and it's running. Called before anything that acts on a pane.
assert_isolated() {
  local st
  st="$(herdr_call status server 2>&1 || true)"
  grep -q 'status: running' <<<"$st" || die "the recon's session is not running"
  grep -q "socket: .*/sessions/$SESSION/herdr.sock" <<<"$st" \
    || die "herdr_call does not reach the recon's own session; refusing"
}

# ---------------------------------------------------------------------------
# Privacy (R3.16): reject any fixture holding private data.
# Values are read at run time, never written into this script.
# ---------------------------------------------------------------------------

privacy_needles() {
  # Fixed path prefixes.
  printf '%s\n' "/Users/" "/var/folders/" "/private/var/folders/"
  # The scratchpads: Claude Code's per-user temp tree and this shell's TMPDIR.
  printf '%s\n' "/tmp/claude-$(id -u)"
  if [[ -n "${TMPDIR:-}" ]]; then printf '%s\n' "${TMPDIR%/}"; fi
  # The owner: login name, full name, hostname, git email.
  local v
  v="$(id -un 2>/dev/null || true)"; [[ -n "$v" ]] && printf '%s\n' "$v"
  v="$(id -F 2>/dev/null || true)"; [[ -n "$v" ]] && printf '%s\n' "$v"
  v="$(hostname -s 2>/dev/null || true)"; [[ -n "$v" ]] && printf '%s\n' "$v"
  v="$(hostname 2>/dev/null || true)"; [[ -n "$v" ]] && printf '%s\n' "$v"
  v="$(git -C "$REPO_ROOT" config --get user.email 2>/dev/null || true)"; [[ -n "$v" ]] && printf '%s\n' "$v"
  v="$(git config --global --get user.email 2>/dev/null || true)"; [[ -n "$v" ]] && printf '%s\n' "$v"
  # The Claude account's email and display name, which a welcome banner can show.
  if [[ -r "$HOME/.claude.json" ]]; then
    jq -r '.oauthAccount // {} | (.emailAddress // empty), (.displayName // empty)' "$HOME/.claude.json" 2>/dev/null || true
  fi
  return 0
}

# privacy_scan <file>: prints one line per private value found (never the value
# itself, only which kind), returns 1 if any.
privacy_scan() {
  local f="$1" bad=0 needle i=0
  while IFS= read -r needle; do
    i=$((i + 1))
    [[ -n "$needle" ]] || continue
    if grep -qiF -- "$needle" "$f"; then
      printf 'privacy: %s: holds private value #%d\n' "$f" "$i"
      bad=1
    fi
  done < <(privacy_needles)
  return "$bad"
}

# save_fixture <dest>: reads stdin, rejects private data, then writes <dest>.
save_fixture() {
  local dest="$1" tmp
  tmp="$(mktemp "${TMPDIR:-/tmp}/recon-fixture.XXXXXX")"
  cat > "$tmp"
  if ! privacy_scan "$tmp" >&2; then
    rm -f "$tmp"
    die "refused to save $dest: it holds private data"
  fi
  mkdir -p "$(dirname "$dest")"
  mv "$tmp" "$dest"
  chmod 0644 "$dest"
}

# ---------------------------------------------------------------------------
# check: offline, no herdr, no session.
# ---------------------------------------------------------------------------

REQUIRED_FILES=(
  "VERSIONS"
  "claude/VERSIONS"
  "claude/KEYS.md"
  "claude/DETECTION.md"
  "claude/list/idle.json"
  "claude/list/working.json"
  "claude/list/blocked-permission.json"
  "claude/list/blocked-question.json"
  "claude/list/done.json"
  "claude/detection/shell-0s.txt"
  "claude/detection/shell-2s.txt"
  "claude/detection/shell-70s.txt"
  "claude/detection/danger.txt"
  "claude/tool-prompts/VERSIONS"
)
# Best effort (R3.17): all three captures, or "not captured: <name>" in DETECTION.md.
BEST_EFFORT_PROMPTS=(file-edit file-create question plan trust)
# The tool prompts (O-88), in claude/tool-prompts/detection: all three
# captures, or "not captured: <name>" in DETECTION.md with the evidence.
TOOL_PROMPTS=(mcp webfetch read-outside)
# One capture each: an MCP call whose argument wraps, and a URL longer than a line.
TOOL_SINGLES=(mcp-long mcp-desc webfetch-long)
# Each tool prompt's box title line, which a capture must start with (O-88's
# review): anchored, so a Bash command the agent falls back to (`curl …`,
# `cat /etc/shells`, `echo …`) is never saved as a tool capture. Each is the
# captures' (check verifies them): the MCP prompt's from the isolated
# session's run (O-90, 2026-11-02), whose box also names the server and the
# tool, "recon — Echo Tool: (MCP)".
TITLE_FETCH='^ Fetch$'
TITLE_READ='^ Read file$'
TITLE_MCP='^ Tool use$'
# tool_title <capture name>: the title its box must start with; "" if none.
tool_title() {
  case "$1" in
    webfetch*) echo "$TITLE_FETCH" ;;
    read-outside*) echo "$TITLE_READ" ;;
    mcp*) echo "$TITLE_MCP" ;;
  esac
}

cmd_check() {
  local dir="${1:-$TESTDATA}" keymap="${2:-$KEYMAP}" fail=0 f p t
  [[ -d "$dir" ]] || { echo "missing: fixture directory $dir"; return 1; }

  for f in "${REQUIRED_FILES[@]}"; do
    [[ -s "$dir/$f" ]] || { echo "missing: $f"; fail=1; }
  done
  local set
  for p in "${BEST_EFFORT_PROMPTS[@]}" "${TOOL_PROMPTS[@]}"; do
    local have=0
    set="claude/detection"
    [[ " ${TOOL_PROMPTS[*]} " == *" $p "* ]] && set="claude/tool-prompts/detection"
    for t in 0s 2s 70s; do [[ -s "$dir/$set/$p-$t.txt" ]] && have=$((have + 1)); done
    if [[ $have -ne 3 ]]; then
      if ! grep -qx "not captured: $p" "$dir/claude/DETECTION.md" 2>/dev/null; then
        echo "missing: $set/$p-{0s,2s,70s}.txt (or 'not captured: $p' in DETECTION.md)"
        fail=1
      fi
    fi
  done
  [[ -s "$keymap" ]] || { echo "missing: keymap $keymap"; fail=1; }

  # Every JSON fixture parses.
  while IFS= read -r -d '' f; do
    jq -e . "$f" >/dev/null 2>&1 || { echo "json: $f does not parse"; fail=1; }
  done < <(find "$dir" -type f -name '*.json' -print0)

  # Each agent.list holds only the recon's own panes: exactly one agent.
  for f in "$dir"/claude/list/*.json; do
    [[ -e "$f" ]] || continue
    local n
    n="$(jq '.result.agents | length' "$f" 2>/dev/null || echo x)"
    [[ "$n" == "1" ]] || { echo "list: $f holds $n agents, want 1"; fail=1; }
  done

  # Every fixture of a set comes from the Claude Code version in that set's
  # VERSIONS (R3.2): no screen may show another version's banner. Phase 0's
  # set is claude/, the tool prompts' claude/tool-prompts/ (O-88).
  local want_v seen_v
  for set in claude claude/tool-prompts; do
    want_v="$(sed -n 's/^claude_version = "\(.*\)"$/\1/p' "$dir/$set/VERSIONS" 2>/dev/null | head -1)"
    if [[ -n "$want_v" && -d "$dir/$set/detection" ]]; then
      while IFS= read -r seen_v; do
        [[ -z "$seen_v" || "$seen_v" == "Claude Code v$want_v" ]] || { echo "version: a capture in $set shows $seen_v, its VERSIONS says $want_v"; fail=1; }
      done < <(grep -rhoE 'Claude Code v[0-9][0-9.]*[0-9]' "$dir/$set/detection" 2>/dev/null | sort -u || true)
    fi
  done

  # Each tool prompt's capture has its tool's box title (O-88's review), so
  # a Bash command the agent fell back to was never saved as one.
  local name want_t
  for f in "$dir"/claude/tool-prompts/detection/*.txt; do
    [[ -e "$f" ]] || continue
    name="$(basename "$f" .txt)"
    case "$name" in *-unexpected) continue ;; esac
    want_t="$(tool_title "$name")"
    [[ -n "$want_t" ]] && LC_ALL=en_US.UTF-8 keymap_box "$f" | head -1 | grep -qE -- "$want_t" \
      || { echo "title: $name's box doesn't start with its tool's title ($want_t)"; fail=1; }
  done

  # Privacy over every fixture and the keymap.
  while IFS= read -r -d '' f; do
    privacy_scan "$f" || fail=1
  done < <(find "$dir" -type f -print0)
  if [[ -f "$keymap" ]]; then privacy_scan "$keymap" || fail=1; fi

  if [[ $fail -eq 0 && -s "$keymap" ]]; then
    check_keymap "$dir" "$keymap" || fail=1
  fi

  if [[ $fail -eq 0 ]]; then echo "check: ok"; else echo "check: FAILED"; fi
  return "$fail"
}

# ---------------------------------------------------------------------------
# The keymap check (R3.15). Patterns are written for Go's RE2 and kept to the
# subset that POSIX ERE reads the same way: no \b, \d, \s, (?:...) or lazy
# quantifiers. command_match may use \n, which the check turns into a newline.
# ---------------------------------------------------------------------------

# The captures of each prompt (R3.10), and which kind each is.
PERMISSION_PROMPTS=(shell file-edit file-create)
PERMISSION_SINGLES=(danger long-prompt shell-repeat)
OTHER_PROMPTS=(question plan trust)
# The exact command each Bash prompt asks for (the script's own fixed text).
command_for() {
  case "$1" in
    shell-*) echo "$CMD_TOUCH" ;;
    danger) echo "$CMD_DANGER" ;;
    long-prompt) echo "$CMD_LONG" ;;
  esac
}

# km_scalar <file> <key>: a single-line '''literal''' or "string" value.
km_scalar() {
  sed -n "s/^$2[[:space:]]*=[[:space:]]*'''\\(.*\\)'''.*/\\1/p; s/^$2[[:space:]]*=[[:space:]]*\"\\([^\"]*\\)\".*/\\1/p" "$1" | head -1
}
# km_keys <file> <key>: the strings of a one-line array, one per line.
km_keys() {
  sed -n "s/^$2[[:space:]]*=[[:space:]]*\\[\\(.*\\)\\].*/\\1/p" "$1" | head -1 \
    | tr ',' '\n' | sed -e 's/^[[:space:]]*"//' -e 's/"[[:space:]]*$//' | grep -v '^[[:space:]]*$' || true
}
# km_list <file> <key>: the '''literal''' entries of a multi-line array.
km_list() {
  awk -v k="$2" '
    $0 ~ "^" k "[[:space:]]*=[[:space:]]*\\[" { on = ($0 !~ /\][[:space:]]*(#.*)?$/); next }
    on && /^\]/ { on = 0 }
    on { s = $0; if (match(s, /'"'''"'.*'"'''"'/)) print substr(s, RSTART + 3, RLENGTH - 6) }
  ' "$1"
}

# matches_any <text> <patterns file>: the text matches one of the patterns.
matches_any() {
  local text="$1" re
  while IFS= read -r re; do
    [[ -n "$re" ]] || continue
    [[ "$text" =~ $re ]] && return 0
  done < "$2"
  return 1
}

# group_diff <a> <b>: lines of a not in b, and of b not in a (empty lines skipped).
group_diff() {
  { grep -vxF -f "$2" "$1" || true; grep -vxF -f "$1" "$2" || true; } | grep -v '^[[:space:]]*$' || true
}

# box_of <file>: the prompt box, from its header line down.
box_of() { prompt_box < "$1"; }

# keymap_box <file>: the prompt box as the bridge's keymap cuts it (box_start):
# every line below the last line drawn only with ─, the box's top rule, so it
# begins with the box's title line ("Bash command").
keymap_box() { awk '/^(─)+$/ { n = NR } { l[NR] = $0 } END { for (i = n + 1; i <= NR; i++) print l[i] }' "$1"; }

# changing_lines <dir>: every line that differs between two captures of the
# same prompt: the prompt boxes (keymap_box) of the 0 s, 2 s and 70 s screens
# of each prompt, and of the first and the repeated shell prompt. Only the box
# counts: the bridge reads nothing above it (box_start, phase 2), where a
# spinner may change (the Read prompt's "⏺ Reading 1 file…", O-88).
changing_lines() {
  local d p a b t
  t="$(mktemp -d "${TMPDIR:-/tmp}/recon-box.XXXXXX")"
  for p in "${PERMISSION_PROMPTS[@]}" "${OTHER_PROMPTS[@]}" "${TOOL_PROMPTS[@]}"; do
    d="$1/claude/detection"
    [[ " ${TOOL_PROMPTS[*]} " == *" $p "* ]] && d="$1/claude/tool-prompts/detection"
    for a in 0s 2s; do
      for b in 2s 70s; do
        [[ "$a" != "$b" && -s "$d/$p-$a.txt" && -s "$d/$p-$b.txt" ]] || continue
        keymap_box "$d/$p-$a.txt" > "$t/a"; keymap_box "$d/$p-$b.txt" > "$t/b"
        group_diff "$t/a" "$t/b"
      done
    done
  done
  d="$1/claude/detection"
  if [[ -s "$d/shell-0s.txt" && -s "$d/shell-repeat.txt" ]]; then
    box_of "$d/shell-0s.txt" > "$t/a"; box_of "$d/shell-repeat.txt" > "$t/b"
    group_diff "$t/a" "$t/b"
  fi
  rm -rf "$t"
}

check_keymap() {
  local dir="$1" km="$2" fail=0 t k v re f p name want got text
  export LC_ALL=en_US.UTF-8
  t="$(mktemp -d "${TMPDIR:-/tmp}/recon-km.XXXXXX")"
  km_list "$km" permission_match > "$t/perm"
  km_list "$km" ignore_match > "$t/ignore"

  # Fields.
  v="$(km_scalar "$km" agent_version)"
  want="$(sed -n 's/^claude_version = "\(.*\)"$/\1/p' "$dir/claude/VERSIONS" | head -1)"
  [[ -n "$v" && "$v" == "$want" ]] || { echo "keymap: agent_version '$v' is not claude/VERSIONS's '$want'"; fail=1; }
  [[ -n "$(km_scalar "$km" captured)" ]] || { echo "keymap: no captured date"; fail=1; }
  grep -q '^\[claude\]' "$km" || { echo "keymap: no [claude] table (AgentInfo.agent)"; fail=1; }

  # Keys: present, each passes the parent design's key check, never ctrl+c.
  for k in approve deny interrupt; do
    [[ -n "$(km_keys "$km" "$k")" ]] || { echo "keymap: $k has no keys"; fail=1; }
    while IFS= read -r v; do
      [[ "$v" =~ ^(esc|enter|tab|up|down|left|right|backspace|[0-9]|[a-z]|ctrl\+[a-z])$ && "$v" != "ctrl+c" ]] \
        || { echo "keymap: $k key '$v' fails the key check"; fail=1; }
    done < <(km_keys "$km" "$k")
  done

  # No pattern depends on the model's name.
  if cat "$t/perm" "$t/ignore" <(km_scalar "$km" command_match) | grep -qiE 'haiku|sonnet|opus|claude-[a-z]+-[0-9]'; then
    echo "keymap: a pattern names a model"; fail=1
  fi
  [[ -s "$t/perm" ]] || { echo "keymap: permission_match is empty"; fail=1; }

  # permission_match: every permission capture, the tool prompts' too (O-88),
  # none of the other prompts.
  for f in "$dir"/claude/detection/*.txt "$dir"/claude/tool-prompts/detection/*.txt; do
    [[ -e "$f" ]] || continue
    name="$(basename "$f" .txt)"
    case "$name" in *-unexpected|*-now) continue ;; esac
    p="${name%-0s}"; p="${p%-2s}"; p="${p%-70s}"
    text="$(cat "$f")"
    if [[ " ${PERMISSION_PROMPTS[*]} ${PERMISSION_SINGLES[*]} ${TOOL_PROMPTS[*]} " == *" $p "* || " ${PERMISSION_SINGLES[*]} ${TOOL_SINGLES[*]} " == *" $name "* ]]; then
      matches_any "$text" "$t/perm" || { echo "keymap: permission_match misses $name"; fail=1; }
    elif [[ " ${OTHER_PROMPTS[*]} " == *" $p "* ]]; then
      matches_any "$text" "$t/perm" && { echo "keymap: permission_match matches $name (not a permission prompt)"; fail=1; }
    fi
  done

  # command_match: extracts exactly the asked-for command from each Bash
  # prompt's box, as the bridge reads it (keymap_box): the pattern is anchored
  # to the box's start and end.
  re="$(km_scalar "$km" command_match)"
  [[ -n "$re" ]] || { echo "keymap: no command_match"; fail=1; }
  re="${re//\\n/$'\n'}"
  for f in "$dir"/claude/detection/shell-*.txt "$dir/claude/detection/danger.txt" "$dir/claude/detection/long-prompt.txt"; do
    [[ -s "$f" && -n "$re" ]] || continue
    name="$(basename "$f" .txt)"; want="$(command_for "$name")"
    got=""
    if [[ "$(keymap_box "$f")" =~ $re ]]; then
      got="${BASH_REMATCH[1]}"; got="${got#"${got%%[![:space:]]*}"}"; got="${got%"${got##*[![:space:]]}"}"
    fi
    [[ "$got" == "$want" ]] || { echo "keymap: command_match on $name gives '$got', want '$want'"; fail=1; }
  done

  # ignore_match: every line that differs between captures of the same prompt.
  while IFS= read -r v; do
    matches_any "$v" "$t/ignore" || { echo "keymap: ignore_match misses a changing line: $v"; fail=1; }
  done < <(changing_lines "$dir")

  rm -rf "$t"
  return "$fail"
}

# diffs: the changing lines that ignore_match must cover (offline).
cmd_diffs() { export LC_ALL=en_US.UTF-8; changing_lines "$TESTDATA" | sort | uniq -c; }

# selftest: check must fail on an empty fixture directory, and on a copy of the
# fixtures holding a planted /Users/ path. Both live in a temp directory that is
# removed afterwards.
cmd_selftest() {
  local scratch rc
  scratch="$(mktemp -d "${TMPDIR:-/tmp}/recon-selftest.XXXXXX")"
  set +e; ( selftest_body "$scratch" ); rc=$?; set -e
  rm -rf "$scratch"
  [[ ! -e "$scratch" ]] || { echo "selftest: FAILED: scratch not removed"; return 1; }
  [[ $rc -eq 0 ]] && echo "selftest: ok (scratch removed)" || echo "selftest: FAILED (scratch removed)"
  return "$rc"
}

selftest_body() {
  local scratch="$1" rc out
  mkdir -p "$scratch/empty"
  set +e; out="$(cmd_check "$scratch/empty" "$scratch/empty/none.toml")"; rc=$?; set -e
  if [[ $rc -eq 0 ]]; then echo "selftest: FAILED: check passed an empty directory"; return 1; fi
  echo "selftest: empty directory -> check failed ($(grep -c '^missing:' <<<"$out") missing)"

  mkdir -p "$scratch/copy"
  if [[ -d "$TESTDATA" ]]; then cp -R "$TESTDATA/." "$scratch/copy/"; fi
  if [[ -f "$KEYMAP" ]]; then cp "$KEYMAP" "$scratch/copy-keymap.toml"; else : > "$scratch/copy-keymap.toml"; fi
  mkdir -p "$scratch/copy/claude/detection"
  # The planted path is built here, so this script holds no home-folder path itself (the publish export refuses one).
  printf 'cwd: /Users/%s/project\n' someone > "$scratch/copy/claude/detection/planted.txt"
  set +e; out="$(cmd_check "$scratch/copy" "$scratch/copy-keymap.toml")"; rc=$?; set -e
  if [[ $rc -eq 0 ]]; then echo "selftest: FAILED: check passed a planted /Users/ path"; return 1; fi
  if ! grep -q '^privacy: .*planted.txt' <<<"$out"; then
    echo "selftest: FAILED: check did not name the planted fixture"; return 1
  fi
  echo "selftest: planted /Users/ -> check failed, naming planted.txt"
  echo "selftest: other findings on the copy: $(grep -vc 'planted.txt\|^check:' <<<"$out" || true)"

  # A keymap whose permission_match matches nothing, and whose command_match
  # extracts the wrong line, must fail the keymap check on the real fixtures.
  if [[ -f "$KEYMAP" && -d "$TESTDATA" ]]; then
    sed -e "s/^  '''Do you want.*/  '''NO-SUCH-PROMPT''',/" -e "s/^command_match = .*/command_match = '''(Bash command)'''/" \
      "$KEYMAP" > "$scratch/broken.toml"
    set +e; out="$(LC_ALL=en_US.UTF-8 check_keymap "$TESTDATA" "$scratch/broken.toml")"; rc=$?; set -e
    if [[ $rc -eq 0 ]]; then echo "selftest: FAILED: check_keymap passed a broken keymap"; return 1; fi
    # Each half of the breakage must be caught on its own: either alone fails the check.
    local misses bad_commands
    misses="$(grep -c 'permission_match misses' <<<"$out" || true)"
    bad_commands="$(grep -c 'command_match on' <<<"$out" || true)"
    if [[ $misses -eq 0 ]]; then echo "selftest: FAILED: check_keymap missed the broken permission_match"; return 1; fi
    if [[ $bad_commands -eq 0 ]]; then echo "selftest: FAILED: check_keymap missed the broken command_match"; return 1; fi
    echo "selftest: broken keymap -> check_keymap failed ($misses misses, $bad_commands bad commands)"
  fi

  # A fetch capture whose box is a Bash command the agent fell back to (its
  # title " Bash command", example.com still in it) must fail the check on
  # its title (O-88's review).
  if [[ -s "$TESTDATA/claude/tool-prompts/detection/webfetch-0s.txt" ]]; then
    mkdir -p "$scratch/fallback"
    cp -R "$TESTDATA/." "$scratch/fallback/"
    sed 's/^ Fetch$/ Bash command/' "$TESTDATA/claude/tool-prompts/detection/webfetch-0s.txt" \
      > "$scratch/fallback/claude/tool-prompts/detection/webfetch-0s.txt"
    set +e; out="$(cmd_check "$scratch/fallback" "$KEYMAP")"; rc=$?; set -e
    if [[ $rc -eq 0 ]] || ! grep -q '^title: webfetch-0s' <<<"$out"; then
      echo "selftest: FAILED: check passed a fetch capture whose box is a Bash command"; return 1
    fi
    echo "selftest: a Bash fallback saved as a fetch capture -> check failed on its title"
  fi

  # onboard's ready check (O-90's review): the input box at the screen's
  # bottom, as at the end of the safe-mode run's capture, and not a dialog,
  # as the fetch prompt is.
  if [[ -s "$TESTDATA/claude/tool-prompts/detection/mcp-safe-mode-unexpected.txt" && -s "$TESTDATA/claude/tool-prompts/detection/webfetch-0s.txt" ]]; then
    input_box_ready < "$TESTDATA/claude/tool-prompts/detection/mcp-safe-mode-unexpected.txt" \
      || { echo "selftest: FAILED: the input box isn't ready"; return 1; }
    if input_box_ready < "$TESTDATA/claude/tool-prompts/detection/webfetch-0s.txt"; then
      echo "selftest: FAILED: a fetch prompt reads as the ready input box"; return 1
    fi
    # Its placeholder, after a no-break space, as the run of 2026-11-02 drew
    # it: still the ready input box.
    sed "s/^❯ *\$/❯$(printf '\302\240')Try \"how does <filepath> work?\"/" \
      "$TESTDATA/claude/tool-prompts/detection/mcp-safe-mode-unexpected.txt" | input_box_ready \
      || { echo "selftest: FAILED: the input box with its placeholder isn't ready"; return 1; }
    echo "selftest: onboard's ready check: the input box yes (empty, or its placeholder), a prompt no"
  fi

  # onboard's Chrome extension screen (the owner's decision, 2026-11-02): the
  # screen as DETECTION.md records it gets esc, with the cursor on either
  # option; nothing else does: not the same text under a sign-in, nor its
  # header left in the scrollback over the input box, nor another dialog
  # under it, nor a prompt or the input box.
  local bullet header body1 body2 no yes foot rule chrome
  bullet="$(grep -F 'Claude in Chrome extension detected' "$CLAUDE_DIR/DETECTION.md" | head -1)"
  header="$(sed -n 's/.*`\(Claude in Chrome extension detected\)`.*/\1/p' <<<"$bullet")"
  no="$(sed -n 's/.*`❯ \(No, keep browser tools off\)`.*/\1/p' <<<"$bullet")"
  yes="$(sed -n 's/.*`\(Yes, use my browser\)`.*/\1/p' <<<"$bullet")"
  foot="$(sed -n 's/.*`\(Enter to confirm · Esc to keep browser tools off\)`.*/\1/p' <<<"$bullet")"
  body1="$(sed -n 's/.*`\(Claude will use your Chrome browser by default[^`]*\)`.*/\1/p' <<<"$bullet")"
  body2="$(sed -n 's/.*`\(Browser actions still go through[^`]*\)`.*/\1/p' <<<"$bullet")"
  if [[ -z "$header" || -z "$no" || -z "$yes" || -z "$foot" || -z "$body1" || -z "$body2" ]]; then
    echo "selftest: FAILED: DETECTION.md doesn't record the Chrome extension screen's text"; return 1
  fi
  rule="$(printf '─%.0s' $(seq 1 40))"
  chrome="$(printf '%s\n' '$ claude' '' "$rule" "  $header" '' "  $body1" '' "  $body2" '' "  ❯ $no" "    $yes" '' "  $foot")"
  chrome_screen <<<"$chrome" || { echo "selftest: FAILED: the Chrome extension screen isn't recognised"; return 1; }
  chrome_screen <<<"$(sed -e "s/^  ❯ $no\$/    $no/" -e "s/^    $yes\$/  ❯ $yes/" <<<"$chrome")" \
    || { echo "selftest: FAILED: the Chrome extension screen with the cursor on Yes isn't recognised"; return 1; }
  local not
  for not in "$chrome"$'\n  Select login method:' \
             "$(printf '%s\n' '$ claude' "  $header" '' "$rule" '❯ ' "$rule" '  ⏸ manual mode on')" \
             "$(printf '%s\n' "$chrome" "$rule" ' Do you want to proceed?' ' ❯ 1. Yes')" \
             "$(cat "$TESTDATA/claude/tool-prompts/detection/webfetch-0s.txt")" \
             "$(cat "$TESTDATA/claude/tool-prompts/detection/mcp-safe-mode-unexpected.txt")"; do
    if chrome_screen <<<"$not"; then
      echo "selftest: FAILED: a screen that isn't the Chrome extension's alone is taken for it"; return 1
    fi
  done
  echo "selftest: onboard's Chrome extension screen: recognised as recorded, and nothing else"

  # The isolated session's arguments (O-90): the clean ones without
  # --safe-mode and nothing else changed (Haiku, project settings only, the
  # stub alone as MCP server, manual mode), the regular session's keeping
  # it; its pane's environment the regular one plus the recon's own sign-in
  # folder, outside the repository and outside the owner's ~/.claude.
  local want got
  want="$(printf '%s\n' "${CLAUDE_ARGS[@]}" | grep -vx -- '--safe-mode')"
  got="$(printf '%s\n' "${CLAUDE_ARGS_ISOLATED[@]}")"
  if [[ "$want" != "$got" ]] || ! printf '%s\n' "${CLAUDE_ARGS[@]}" | grep -qx -- '--safe-mode'; then
    echo "selftest: FAILED: the isolated session's arguments aren't the clean ones without --safe-mode"; return 1
  fi
  for want in "--model claude-haiku-5-5" "--setting-sources project" "--strict-mcp-config --mcp-config $MCP_CONFIG" "--permission-mode manual"; do
    [[ " ${CLAUDE_ARGS_ISOLATED[*]} " == *" $want "* ]] || { echo "selftest: FAILED: the isolated session lacks $want"; return 1; }
  done
  want="$(printf '%s\n' "${PANE_ENV[@]}" "--env" "CLAUDE_CONFIG_DIR=$ISOLATED_CONFIG")"
  got="$(printf '%s\n' "${PANE_ENV_ISOLATED[@]}")"
  if [[ "$want" != "$got" || "$ISOLATED_CONFIG" != "$HOME/.herdr-pager-recon/claude" ||
        "$ISOLATED_CONFIG" == "$REPO_ROOT"/* || "$ISOLATED_CONFIG" == "$HOME/.claude"* ||
        "$ISOLATED_CONFIG" == "$STATE_DIR"/* || "$ISOLATED_CONFIG" == "$RECON_DIR"/* ]]; then
    echo "selftest: FAILED: the isolated pane's environment isn't the regular one plus the recon's own sign-in folder"; return 1
  fi
  echo "selftest: the isolated session: the clean arguments without --safe-mode, the recon's own sign-in folder"
}

# ---------------------------------------------------------------------------
# help-topic: herdr's help text, through herdr_call, for fixed topics only.
# ---------------------------------------------------------------------------

cmd_help_topic() {
  local t
  [[ $# -gt 0 ]] || set -- ""
  for t in "$@"; do
    echo "=== ${t:-herdr}"
    help_one "$t" 2>&1 | grep -v -e 'Are you an AI' -e 'herdr.dev/' -e 'SKIP if a Herdr' -e 'Control Herdr panes' || true
  done
}

help_one() {
  case "${1:-}" in
    "")        herdr_call --help ;;
    agent)     herdr_call agent --help ;;
    session)   herdr_call session --help ;;
    server)    herdr_call server --help ;;
    workspace) herdr_call workspace --help ;;
    pane)      herdr_call pane --help ;;
    api)       herdr_call api --help ;;
    tab)       herdr_call tab --help ;;
    skill)     herdr_call --skill ;;
    agent.start|agent.read|agent.send-keys|agent.prompt|agent.wait|agent.explain|\
    agent.list|agent.get|agent.focus|session.stop|session.delete|session.list|\
    workspace.create|workspace.list|workspace.close|pane.list|pane.split|pane.close|\
    api.schema|tab.create|tab.focus|pane.get)
      herdr_call "${1%%.*}" "${1#*.}" --help ;;
    *) die "unknown help topic" ;;
  esac
}

# clear_fixtures: a capture run starts by deleting this script's own outputs, so
# no fixture from an earlier Claude Code version survives (R3.2). KEYS.md,
# DETECTION.md and the keymap are written by hand from the captures; they stay.
clear_fixtures() {
  rm -rf -- "$CLAUDE_DIR/list" "$CLAUDE_DIR/detection" "$CLAUDE_DIR/explain" "$CLAUDE_DIR/get"
  rm -f -- "$TESTDATA/VERSIONS" "$CLAUDE_DIR/VERSIONS"
  log "cleared the earlier run's captures (list, detection, explain, get, VERSIONS)"
  clear_tool_fixtures
}

# clear_tool_fixtures: the tool prompts' set (O-88) alone.
clear_tool_fixtures() {
  rm -rf -- "$TOOL_DIR/detection" "$TOOL_DIR/explain" "$TOOL_DIR/get"
  rm -f -- "$TOOL_DIR/VERSIONS"
  log "cleared the earlier run's tool prompt captures (tool-prompts: detection, explain, get, VERSIONS)"
}

# write_tool_versions: the tool prompts' VERSIONS, Claude Code's and herdr's
# versions now (R3.2: every capture of the set comes from them).
write_tool_versions() {
  local hv cv
  hv="$(herdr_call --version | head -1)"
  cv="$(claude --version | head -1)"
  [[ -n "$hv" && -n "$cv" ]] || die "could not read herdr's or Claude Code's version"
  {
    echo "# Claude Code and herdr, as seen by the recon's tool prompts (capture-claude.sh tool-versions)"
    echo "claude_version = \"${cv%% *}\""
    echo "claude_version_line = \"$cv\""
    echo "herdr_version = \"${hv#herdr }\""
  } | save_fixture "$TOOL_DIR/VERSIONS"
  echo "tool-versions: claude ${cv%% *}, herdr ${hv#herdr }"
}

# tool-versions: the first step of a run that captures only the tool prompts
# (O-88), after a Claude Code release, keeping phase 0's set: it clears the
# tool prompts' old captures and records the versions they come from.
cmd_tool_versions() {
  clear_tool_fixtures
  write_tool_versions
}

# versions: herdr's version and protocol, and Claude Code's version (R3.2).
# It is the first step of a capture run, so it clears the old captures first.
# The AgentInfo.agent string is added by `trust`, once herdr has detected it.
cmd_versions() {
  local hv proto sv cv
  clear_fixtures
  hv="$(herdr_call --version | head -1)"
  proto="$(herdr_call api schema --json | jq -r '.protocol')"
  sv="$(herdr_call api schema --json | jq -r '.schema_version // empty')"
  [[ -n "$hv" && -n "$proto" && "$proto" != null ]] || die "could not read herdr's version or protocol"
  {
    echo "# herdr, as seen by the recon (capture-claude.sh versions)"
    echo "herdr_version = \"${hv#herdr }\""
    echo "protocol = $proto"
    [[ -n "$sv" ]] && echo "schema_version = $sv"
    echo "source = \"herdr --version; herdr api schema --json (.protocol, .schema_version)\""
  } | save_fixture "$TESTDATA/VERSIONS"
  cv="$(claude --version | head -1)"
  [[ -n "$cv" ]] || die "could not read claude --version"
  {
    echo "# Claude Code, as seen by the recon (capture-claude.sh versions, start)"
    echo "claude_version = \"${cv%% *}\""
    echo "claude_version_line = \"$cv\""
  } | save_fixture "$CLAUDE_DIR/VERSIONS"
  echo "versions: herdr ${hv#herdr }, protocol $proto; claude ${cv%% *}"
  write_tool_versions
}

# server-up: start the recon's own headless server, with no window attached,
# past the nesting guard by the clean environment in herdr_call (R3.3).
cmd_server_up() {
  local st i
  st="$(herdr_call status server 2>&1 || true)"
  if grep -q 'status: running' <<<"$st"; then
    assert_isolated
    echo "server-up: the recon's session was already running"
    return 0
  fi
  herdr_call --detach server
  for i in $(seq 1 40); do
    sleep 0.5
    st="$(herdr_call status server 2>&1 || true)"
    grep -q 'status: running' <<<"$st" && break
  done
  if ! grep -q 'status: running' <<<"$st"; then
    echo "server-up: FAILED; status says:"
    grep -v 'socket:' <<<"$st" | head -12
    return 1
  fi
  assert_isolated
  echo "server-up: running on the recon's own session"
  grep -E 'status:|version:|private_protocol:' <<<"$st" | sed 's/^ */  /'
}

# ---------------------------------------------------------------------------
# The recon's own state.
# ---------------------------------------------------------------------------

state_get() { [[ -f "$STATE_DIR/$1" ]] || die "no state '$1' (run start first)"; cat "$STATE_DIR/$1"; }
state_set() { printf '%s' "$2" > "$STATE_DIR/$1"; }
pane_id() { state_get pane; }

# A herdr id is all this script passes besides its own fixed text.
is_pane_id() { [[ "$1" =~ ^w[0-9]+:p[0-9]+$ ]]; }
is_ws_id() { [[ "$1" =~ ^w[0-9]+$ ]]; }

# within_limits: the 30-minute limit, counted from Claude Code's start (R3.6).
within_limits() {
  local started now
  started="$(state_get started)"; now="$(date +%s)"
  (( now - started <= MAX_SECONDS )) || die "the 30-minute limit has passed; clean up"
}

# send_prompt <fixed text>: one message to the nested agent, counted (R3.6).
send_prompt() {
  local n
  phase0_only
  within_limits
  n="$(state_get messages)"
  (( n < MAX_MESSAGES )) || die "the 25-message limit is spent"
  state_set messages "$((n + 1))"
  log "message $((n + 1)) of $MAX_MESSAGES"
  # --wait: herdr first requires an observed working or blocked state, then
  # matches idle, done or blocked, so an agent that was already idle can't
  # satisfy the wait before the prompt takes effect.
  herdr_call agent prompt "$AGENT" "$1" --wait --timeout 90000 >/dev/null 2>&1 || true
}

# start [messages-used]: the recon folder, a workspace with the agent's pane, a
# second tab with the plain shell pane (focused), and Claude Code (R3.5).
# messages-used carries the messages an earlier, stopped run of the same
# capture already sent, so the 25-message limit holds across both (R3.6).
cmd_start() { start_session 0 "$@"; }

# start-isolated [messages-used]: the same, for the MCP prompt alone (O-90):
# Claude Code without --safe-mode, in the recon's own sign-in folder.
cmd_start_isolated() { start_session 1 "$@"; }

# check_recon_login: the recon's own sign-in folder (O-90) is a real folder,
# not a link, owned by this user and closed to others (0700), and the owner
# has signed in there (it isn't empty). Only its names are listed; no file in
# it is read.
check_recon_login() {
  local how="sign in once, in your own terminal: mkdir -p $ISOLATED_CONFIG_SHOWN && chmod 700 ~/.herdr-pager-recon $ISOLATED_CONFIG_SHOWN && CLAUDE_CONFIG_DIR=$ISOLATED_CONFIG_SHOWN claude (then /exit)"
  [[ -d "$ISOLATED_CONFIG" && ! -L "$ISOLATED_CONFIG" ]] || die "start: no recon sign-in at $ISOLATED_CONFIG_SHOWN: $how"
  [[ -O "$ISOLATED_CONFIG" ]] || die "start: $ISOLATED_CONFIG_SHOWN isn't owned by you"
  local mode
  mode="$(stat -f '%Lp' "$ISOLATED_CONFIG" 2>/dev/null || stat -c '%a' "$ISOLATED_CONFIG")"
  [[ "$mode" == 700 ]] || die "start: $ISOLATED_CONFIG_SHOWN is mode $mode; make it 0700 (chmod 700 $ISOLATED_CONFIG_SHOWN)"
  [[ -n "$(ls -A "$ISOLATED_CONFIG")" ]] || die "start: $ISOLATED_CONFIG_SHOWN is empty: $how"
}

# session_isolated: this run's session was started with start-isolated.
session_isolated() { [[ -f "$STATE_DIR/isolated" && "$(cat "$STATE_DIR/isolated")" == 1 ]]; }

# phase0_only: refuse an isolated session (O-90), unless the MCP
# subcommands, the only ones meant for it, said so (ALLOW_ISOLATED=1).
ALLOW_ISOLATED=0
phase0_only() {
  if session_isolated && [[ "$ALLOW_ISOLATED" != 1 ]]; then
    die "this session runs without --safe-mode (start-isolated): only onboard, mcp and mcp-long may use it"
  fi
}

# start_session <isolated 0|1> [messages-used]
start_session() {
  assert_isolated
  local isolated="$1" used="${2:-0}"
  [[ "$used" =~ ^[0-9]+$ ]] || die "start: messages-used must be a number"
  [[ "$isolated" != 1 ]] || check_recon_login # before anything is made, so a refusal leaves nothing behind
  [[ ! -e "$RECON_DIR" ]] || die "$RECON_DIR exists: an earlier run didn't clean up"
  [[ ! -e "$STATE_DIR" ]] || die "$STATE_DIR exists: an earlier run didn't clean up"
  mkdir "$RECON_DIR" "$STATE_DIR"
  state_set messages "$used"
  state_set isolated "$isolated"
  mcp_stub_up

  local out ws pane tab0 tab shell
  if [[ "$isolated" == 1 ]]; then
    echo "start: isolated: Claude Code without --safe-mode, CLAUDE_CONFIG_DIR the recon's own sign-in ($ISOLATED_CONFIG_SHOWN)"
    out="$(herdr_call workspace create --cwd "$RECON_DIR" --label recon --no-focus "${PANE_ENV_ISOLATED[@]}")"
  else
    out="$(herdr_call workspace create --cwd "$RECON_DIR" --label recon --no-focus "${PANE_ENV[@]}")"
  fi
  ws="$(jq -r '[.. | strings | select(test("^w[0-9]+$"))][0] // empty' <<<"$out")"
  pane="$(jq -r '[.. | strings | select(test("^w[0-9]+:p[0-9]+$"))][0] // empty' <<<"$out")"
  tab0="$(jq -r '[.. | strings | select(test("^w[0-9]+:t[0-9]+$"))][0] // empty' <<<"$out")"
  is_ws_id "$ws" || die "no workspace id in workspace create's output"
  is_pane_id "$pane" || die "no pane id in workspace create's output"
  state_set workspace "$ws"; state_set pane "$pane"

  # The plain shell pane: a second tab in the recon's workspace, focused, so the
  # agent's pane is never the focused one except during the focus check (R3.5,
  # R3.14). A tab, not a split, keeps the agent's pane at its full size.
  out="$(herdr_call tab create --workspace "$ws" --cwd "$RECON_DIR" --label shell --focus "${PANE_ENV[@]}")"
  tab="$(jq -r --arg t "$tab0" '[.. | strings | select(test("^w[0-9]+:t[0-9]+$")) | select(. != $t)][0] // empty' <<<"$out")"
  shell="$(jq -r --arg a "$pane" '[.. | strings | select(test("^w[0-9]+:p[0-9]+$")) | select(. != $a)][0] // empty' <<<"$out")"
  [[ "$tab" =~ ^w[0-9]+:t[0-9]+$ ]] || die "no tab id in tab create's output"
  is_pane_id "$shell" || die "no pane id in tab create's output"
  state_set shell_tab "$tab"; state_set shell "$shell"
  echo "start: workspace $ws, agent pane $pane, shell pane $shell (tab $tab)"

  state_set started "$(date +%s)"
  set +e
  if [[ "$isolated" == 1 ]]; then
    out="$(herdr_call agent start "$AGENT" --kind claude --pane "$pane" --timeout 45000 -- "${CLAUDE_ARGS_ISOLATED[@]}" 2>&1)"
  else
    out="$(herdr_call agent start "$AGENT" --kind claude --pane "$pane" --timeout 45000 -- "${CLAUDE_ARGS[@]}" 2>&1)"
  fi
  local rc=$?
  set -e
  echo "start: agent start exited $rc: $(head -c 300 <<<"$out" | tr '\n' ' ')"
  focus_shell
  echo "start: agent pane focused: $(agent_focused)"
  autoupdater_check
}

# mcp_stub_up: build the recon's stub MCP server (bridge/recon/mcpstub: the
# repository's own Go, standard library only, stdio, no network) into the
# state folder, and write the --mcp-config that names it, with its call log
# there too (O-88). --strict-mcp-config keeps every other MCP server out.
mcp_stub_up() {
  ( cd "$REPO_ROOT/bridge" && go build -o "$MCP_STUB" ./recon/mcpstub ) || die "could not build the stub MCP server"
  rm -f -- "$MCP_LOG"
  jq -n --arg cmd "$MCP_STUB" --arg log "$MCP_LOG" \
    '{mcpServers: {recon: {type: "stdio", command: $cmd, args: ["--log", $log]}}}' > "$MCP_CONFIG"
  echo "start: the stub MCP server is built; --mcp-config $MCP_CONFIG"
}

# mcp_calls: how many tool calls the stub has logged.
mcp_calls() { if [[ -f "$MCP_LOG" ]]; then grep -c . "$MCP_LOG" || true; else echo 0; fi; }

# focus_shell: focus the shell pane's tab.
focus_shell() { herdr_call tab focus "$(state_get shell_tab)" >/dev/null; }

# agent_focused: herdr's `focused` for the agent's pane.
agent_focused() {
  herdr_call agent list | jq -r --arg p "$(pane_id)" '[.result.agents[] | select(.pane_id == $p) | .focused | tostring][0] // "absent"'
}

# autoupdater_check: the nested Claude Code runs with DISABLE_AUTOUPDATER=1.
# Prints counts only, never the environment itself.
autoupdater_check() {
  local lines n with safe config
  lines="$(ps -E -ww -A -o command= 2>/dev/null | grep -F -- '--strict-mcp-config' \
            | grep -F -- '--permission-mode manual' | grep -vF -- 'agent start' || true)"
  n="$(grep -c . <<<"$lines" || true)"
  with="$(grep -c 'DISABLE_AUTOUPDATER=1' <<<"$lines" || true)"
  safe="$(grep -cF -- '--safe-mode' <<<"$lines" || true)"
  config="$(grep -cF -- "CLAUDE_CONFIG_DIR=$ISOLATED_CONFIG" <<<"$lines" || true)"
  echo "start: nested Claude Code processes: $n; with DISABLE_AUTOUPDATER=1: $with; with --safe-mode: $safe; with the isolated CLAUDE_CONFIG_DIR: $config"
}

# ---------------------------------------------------------------------------
# Captures.
# ---------------------------------------------------------------------------

# redact_home: herdr's explanation names its manifest file under the owner's
# home ("manifest_source": "remote:<home>/.local/state/..."). Replace every
# occurrence of the home directory with "~" (deterministic; DETECTION.md says so).
redact_home() {
  jq --arg h "$HOME" 'walk(if type == "string" then (split($h) | join("~")) else . end)'
}

status_now() { herdr_call agent get "$AGENT" | jq -r '.. | objects | select(has("agent_status")) | .agent_status' | head -1; }

# wait_status <ms> <state>...: herdr's own wait; prints the state reached.
wait_status() {
  local ms="$1"; shift
  local args=() s
  for s in "$@"; do args+=(--until "$s"); done
  herdr_call agent wait "$AGENT" "${args[@]}" --timeout "$ms" >/dev/null 2>&1 || true
  status_now
}

detection_text() { herdr_call agent read "$AGENT" --source detection --format text; }

# capture_list <name>: agent.list, holding only the recon's own pane.
capture_list() {
  phase0_only
  herdr_call agent list | jq --arg p "$(pane_id)" '.result.agents |= map(select(.pane_id == $p))' \
    | save_fixture "$CLAUDE_DIR/list/$1.json"
  log "list/$1.json: $(jq -r '.result.agents[0].agent_status' "$CLAUDE_DIR/list/$1.json")"
}

# capture_stem <stem>: the detection text, herdr's explanation, agent.get,
# into CAPTURE_DIR (phase 0's set, or the tool prompts').
capture_stem() {
  phase0_only
  local stem="$1"
  detection_text | save_fixture "$CAPTURE_DIR/detection/$stem.txt"
  herdr_call agent explain "$AGENT" --json | redact_home | save_fixture "$CAPTURE_DIR/explain/$stem.json"
  herdr_call agent get "$AGENT" | save_fixture "$CAPTURE_DIR/get/$stem.json"
  log "detection/$stem.txt: $(jq -r '.state + " by " + (.matched_rule.id // "none")' "$CAPTURE_DIR/explain/$stem.json")"
}

# capture_once <name> <tag>: one capture, named <name>-<tag>.
capture_once() { capture_stem "$1-$2"; }

agent_seq() { herdr_call agent get "$AGENT" | jq -r '[.. | objects | select(has("state_change_seq")) | .state_change_seq][0]'; }

# capture_three <name>: at 0 s, 2 s and 70 s (R3.10).
capture_three() {
  local name="$1"
  capture_once "$name" 0s
  sleep 2
  capture_once "$name" 2s
  sleep 68
  within_limits
  capture_once "$name" 70s
}

# send_keys <key>...: only fixed key names from this script.
send_keys() {
  local k
  for k in "$@"; do
    [[ "$k" =~ ^(esc|enter|tab|shift\+tab|backtab|up|down|[0-9])$ ]] || die "key not allowed: $k"
  done
  within_limits
  herdr_call agent send-keys "$AGENT" "$@" >/dev/null
  log "sent keys: $*"
}

# selected_line: the menu line the cursor is on, in the current detection text.
selected_line() { detection_text | grep -m1 '❯' | sed 's/^ *//' || true; }

# trust: the folder-trust prompt, three captures, then accept it (our own folder).
cmd_trust() {
  assert_isolated
  local st
  st="$(wait_status 30000 blocked)"
  [[ "$st" == "blocked" ]] || die "trust: the agent is $st, not blocked on the trust prompt"
  detection_text | grep -q 'trust this folder' || die "trust: no folder-trust prompt on screen"
  capture_three trust

  # Accept: move to "Yes, I trust this folder" and confirm, checking the cursor.
  if ! selected_line | grep -q 'Yes, I trust this folder'; then send_keys down; sleep 0.5; fi
  selected_line | grep -q 'Yes, I trust this folder' || die "trust: could not select 'Yes, I trust this folder'"
  send_keys enter
  st="$(wait_status 60000 idle done)"
  echo "trust: after accepting, the agent is $st"
  [[ "$st" == "idle" || "$st" == "done" ]] || die "trust: not idle after accepting"
  record_idle
}

# idle: when Claude Code starts without a trust prompt (it remembers trusting
# this folder from an earlier run), capture idle directly, before any task.
cmd_idle() {
  assert_isolated
  local st
  st="$(status_now)"
  [[ "$st" == "idle" ]] || die "idle: the agent is $st, not idle"
  detection_text | grep -q 'trust this folder' && die "idle: a trust prompt is showing; run trust"
  record_idle
}

# record_idle: list/idle.json, and the AgentInfo.agent string into claude/VERSIONS.
record_idle() {
  capture_list idle
  local agent_str
  agent_str="$(jq -r '.result.agents[0].agent' "$CLAUDE_DIR/list/idle.json")"
  grep -v '^agent = ' "$CLAUDE_DIR/VERSIONS" > "$STATE_DIR/versions.tmp" || true
  { cat "$STATE_DIR/versions.tmp"; echo "agent = \"$agent_str\"  # AgentInfo.agent, from agent.list"; } \
    | save_fixture "$CLAUDE_DIR/VERSIONS"
  echo "trust: AgentInfo.agent = $agent_str"
}

# The fixed prompts this script sends (R3.1: its own fixed text only). Each fits
# on one line of the pane, so the echoed prompt never wraps the command onto a
# line of its own.
PROMPT_TOUCH="Use the Bash tool to run exactly this shell command, with nothing added and nothing else: touch approved.txt"
CMD_TOUCH="touch approved.txt"
PROMPT_LONG="Use the Bash tool to run exactly this command in the foreground, nothing else: ping -c 100 127.0.0.1"
CMD_LONG="ping -c 100 127.0.0.1"
PROMPT_QUESTION='Use the AskUserQuestion tool to ask me one question, "Which colour?", with exactly two options, "Red" and "Blue". Do nothing else.'
PROMPT_EDIT="Use the Edit tool to change the word hello to goodbye in notes.txt. Do nothing else."
PROMPT_CREATE="Use the Write tool to create a new file named created.txt that contains the single word hello. Do nothing else."
PROMPT_PLAN="Plan how to create a file named plan.txt containing the word hello, in two lines, then ask me to approve the plan."
PROMPT_DANGER="Use the Bash tool to run exactly this command in the throwaway test repo, nothing else: git -C work push --force"
CMD_DANGER="git -C work push --force"
PROMPT_FINISH="Reply with the single word ok, and use no tools."

file_exists() { [[ -e "$RECON_DIR/$1" ]] && echo yes || echo no; }

# footer_has <text>: the mode line at the bottom of the screen shows <text>.
footer_has() { detection_text | tail -n 4 | grep -qF -- "$1"; }

# mode_to <footer text>: shift+tab through Claude Code's modes until the footer
# shows <footer text>; at most five presses. No prompt is ever sent on the way.
mode_to() {
  local want="$1" i key="shift+tab"
  for i in 1 2 3 4 5; do
    footer_has "$want" && return 0
    if ! herdr_call agent send-keys "$AGENT" "$key" >/dev/null 2>&1; then
      key="backtab"; send_keys "$key"
    else
      log "sent keys: $key"
    fi
    sleep 1
  done
  footer_has "$want"
}

# expect_blocked <name> <ERE>: the agent is blocked and the screen matches
# <ERE>. Otherwise it saves the screen as <name>-unexpected, sends esc, and
# returns non-zero.
expect_blocked() {
  local name="$1" re="$2" st
  st="$(status_now)"
  sleep 1
  if [[ "$st" == "blocked" ]] && detection_text | grep -qE -- "$re"; then return 0; fi
  echo "$name: not the expected prompt (the agent is $st); saving the screen as $name-unexpected and sending esc"
  capture_stem "$name-unexpected" || true
  send_keys esc
  return 4
}

# prompt_box: the detection text from the permission prompt's header down.
prompt_box() { sed -n '/Bash command\|Do you want to\|Edit file\|Create file\|Write(/,$p'; }

# shows_command <cmd>: the prompt on screen shows exactly <cmd> on a line of
# its own (R3.8). Borders and indentation are trimmed; nothing else is.
shows_command() {
  local cmd="$1"
  detection_text | sed -e 's/^[[:space:]│|]*//' -e 's/[[:space:]│|]*$//' | grep -qxF -- "$cmd"
}

# approve: send the approve key; if the prompt doesn't resolve, add enter
# (pager design: approve becomes ["1","enter"] if "1" only moves the selection).
# The enter goes only if the agent is still blocked with the same
# state_change_seq, so it can never land on a different, later prompt.
approve() {
  local seq; seq="$(agent_seq)"
  send_keys 1
  sleep 2
  if [[ "$(status_now)" == "blocked" && "$(agent_seq)" == "$seq" ]]; then
    send_keys enter
    state_set approve_keys '["1","enter"]'
  else
    state_set approve_keys '["1"]'
  fi
  log "approve keys: $(state_get approve_keys)"
}

# shell: 4.4. touch asks permission (R3.7), three captures, approve (the file
# must then exist), and done with the shell pane focused (R3.9).
cmd_shell() {
  assert_isolated
  local st
  st="$(status_now)"
  [[ "$st" == "idle" || "$st" == "done" ]] || die "shell: the agent is $st, not idle"
  [[ ! -e "$RECON_DIR/approved.txt" ]] || die "shell: approved.txt exists already"
  send_prompt "$PROMPT_TOUCH"
  st="$(status_now)"
  if [[ "$st" != "blocked" ]]; then
    echo "shell: STOP (R3.7): touch did not ask permission (the agent is $st; approved.txt exists: $(file_exists approved.txt))"
    return 3
  fi
  sleep 1
  if ! shows_command "$CMD_TOUCH"; then
    echo "shell: the prompt doesn't show exactly '$CMD_TOUCH'; denying it (esc)"
    capture_stem shell-unexpected || true
    send_keys esc
    return 4
  fi
  capture_list blocked-permission
  capture_three shell
  shows_command "$CMD_TOUCH" || die "shell: the prompt changed during the captures"
  [[ ! -e "$RECON_DIR/approved.txt" ]] || die "shell: approved.txt appeared before the approve"
  approve
  st="$(wait_status 90000 done idle)"
  echo "shell: after approve, the agent is $st; approved.txt exists: $(file_exists approved.txt); agent pane focused: $(agent_focused)"
  [[ -e "$RECON_DIR/approved.txt" ]] || { echo "shell: FAILED: approve did not run touch"; return 5; }
  if [[ "$st" != "done" ]]; then
    echo "shell: STOP (R3.17): the agent went to $st, not done, with the shell pane focused"
    capture_list "after-shell-$st"
    return 6
  fi
  capture_list done
}

# shell-again: the same touch, asked again: a second, identical prompt must
# appear (approve didn't pick "don't ask again", R3.13), then approve it.
cmd_shell_again() {
  assert_isolated
  local st first second
  send_prompt "$PROMPT_TOUCH"
  st="$(status_now)"
  if [[ "$st" != "blocked" ]]; then
    echo "shell-again: STOP: no second prompt (the agent is $st): approve picked \"don't ask again\""
    return 3
  fi
  sleep 1
  shows_command "$CMD_TOUCH" || { echo "shell-again: the prompt doesn't show exactly '$CMD_TOUCH'; denying"; send_keys esc; return 4; }
  capture_stem shell-repeat
  first="$(prompt_box < "$CLAUDE_DIR/detection/shell-0s.txt")"
  second="$(prompt_box < "$CLAUDE_DIR/detection/shell-repeat.txt")"
  if [[ "$first" == "$second" ]]; then echo "shell-again: the second prompt is identical to the first"
  else echo "shell-again: the second prompt's box differs from the first's (see detection/shell-repeat.txt)"; fi
  approve
  st="$(wait_status 90000 done idle)"
  echo "shell-again: after approve, the agent is $st"
}

# working: 4.5. A long-running command, approved; one capture while it runs;
# then interrupt with esc, which must return the agent to idle.
cmd_working() {
  assert_isolated
  local st
  send_prompt "$PROMPT_LONG"
  st="$(status_now)"
  if [[ "$st" == "blocked" ]]; then
    sleep 1
    shows_command "$CMD_LONG" || { echo "working: the prompt doesn't show exactly '$CMD_LONG'; denying (esc)"; capture_stem long-unexpected || true; send_keys esc; return 4; }
    capture_stem long-prompt
    approve
    st="$(wait_status 20000 working)"
  else
    echo "working: no permission prompt for '$CMD_LONG' (the agent is $st)"
  fi
  [[ "$st" == "working" ]] || { echo "working: FAILED: the agent is $st, not working"; return 5; }
  sleep 3
  capture_list working
  capture_stem working-now
  send_keys esc
  st="$(wait_status 30000 idle done)"
  echo "working: after esc, the agent is $st"
  capture_stem interrupted-now
  # With the shell pane focused, herdr reports any ended turn, an interrupted
  # one too, as done (unseen) while its own detection reads idle. Either way the
  # agent has left working and Claude Code is back at its prompt.
  local detected; detected="$(jq -r '.state' "$CLAUDE_DIR/explain/interrupted-now.json")"
  echo "working: herdr's detection after esc: $detected"
  [[ "$st" == "idle" || ( "$st" == "done" && "$detected" == "idle" ) ]] \
    || { echo "working: FAILED: interrupt left the agent $st (detection $detected)"; return 6; }
  echo "working: interrupt ok: the agent left working (agent_status $st, detection $detected)"
}

# question: 4.6. A two-option question from the question tool; blocked in
# agent.list; three captures; answered with its first option.
cmd_question() {
  assert_isolated
  local st seq
  send_prompt "$PROMPT_QUESTION"
  expect_blocked question '1\. *Red' || return $?
  detection_text | grep -qE '2\. *Blue' || { echo "question: no second option on screen"; capture_stem question-unexpected; send_keys esc; return 4; }
  capture_list blocked-question
  capture_three question
  seq="$(agent_seq)"
  send_keys 1
  sleep 2
  if [[ "$(status_now)" == "blocked" && "$(agent_seq)" == "$seq" ]]; then send_keys enter; fi
  st="$(wait_status 60000 done idle)"
  echo "question: after answering, the agent is $st"
}

# file-edit, file-create: 4.6. The prompts for an edit and a new file; three
# captures each; denied with esc. Neither file may change.
cmd_file_edit() {
  assert_isolated
  local st
  printf 'hello\n' > "$RECON_DIR/notes.txt"
  send_prompt "$PROMPT_EDIT"
  expect_blocked file-edit 'Do you want' || return $?
  capture_three file-edit
  send_keys esc
  st="$(wait_status 30000 idle done)"
  echo "file-edit: after esc, the agent is $st; notes.txt unchanged: $(grep -qx hello "$RECON_DIR/notes.txt" && echo yes || echo no)"
}

cmd_file_create() {
  assert_isolated
  local st
  send_prompt "$PROMPT_CREATE"
  expect_blocked file-create 'Do you want' || return $?
  capture_three file-create
  send_keys esc
  st="$(wait_status 30000 idle done)"
  echo "file-create: after esc, the agent is $st; created.txt exists: $(file_exists created.txt)"
}

# plan: 4.6. Plan mode, a short plan, the plan-approval prompt (three captures),
# declined with esc; then back to manual mode.
cmd_plan() {
  assert_isolated
  local st rc=0
  mode_to "plan mode on" || { echo "plan: could not reach plan mode"; mode_to "manual mode on" || true; return 5; }
  send_prompt "$PROMPT_PLAN"
  if expect_blocked plan '[Pp]roceed|[Aa]pprove|keep planning'; then
    capture_three plan
    send_keys esc
    st="$(wait_status 30000 idle done)"
    echo "plan: after esc, the agent is $st; plan.txt exists: $(file_exists plan.txt)"
  else
    rc=$?
  fi
  sleep 1
  mode_to "manual mode on" && echo "plan: back in manual mode" || { echo "plan: FAILED to return to manual mode"; return 7; }
  return "$rc"
}

# danger: 4.7. A throwaway bare repository and a clone with a rewritten commit;
# git push --force asks; one capture; deny with esc; the branch must not move.
# The script's own git ignores the owner's git config.
recon_git() {
  env GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 git -c user.name=recon \
    -c user.email=recon@example.invalid -c init.defaultBranch=main -c commit.gpgsign=false "$@"
}

cmd_danger() {
  assert_isolated
  local st bare="$RECON_DIR/remote.git" work="$RECON_DIR/work" before after
  footer_has "manual mode on" || die "danger: not in manual mode"
  [[ ! -e "$bare" && ! -e "$work" ]] || die "danger: the throwaway repositories exist already"
  recon_git init -q --bare "$bare"
  recon_git clone -q "$bare" "$work" 2>/dev/null
  printf 'one\n' > "$work/file.txt"
  recon_git -C "$work" add file.txt
  recon_git -C "$work" commit -q -m one
  recon_git -C "$work" push -q -u origin main 2>/dev/null
  printf 'two\n' > "$work/file.txt"
  recon_git -C "$work" commit -q -a --amend -m two
  before="$(recon_git --git-dir "$bare" rev-parse refs/heads/main)"
  [[ "$before" != "$(recon_git -C "$work" rev-parse HEAD)" ]] || die "danger: the clone doesn't diverge"
  send_prompt "$PROMPT_DANGER"
  st="$(status_now)"
  sleep 1
  if [[ "$st" != "blocked" ]] || ! shows_command "$CMD_DANGER"; then
    echo "danger: not the expected prompt (the agent is $st); saving danger-unexpected, sending esc"
    capture_stem danger-unexpected || true
    send_keys esc
    return 4
  fi
  capture_stem danger
  send_keys esc
  st="$(wait_status 30000 idle done)"
  after="$(recon_git --git-dir "$bare" rev-parse refs/heads/main)"
  echo "danger: after esc, the agent is $st"
  if [[ "$before" == "$after" ]]; then echo "danger: deny held: the bare repository's main has not moved"
  else echo "danger: FAILED: the bare repository's main moved"; return 6; fi
}

# ---------------------------------------------------------------------------
# The tool prompts (open item O-88), into claude/tool-prompts/. Each is
# captured, then denied with esc: the recon never approves one. Run
# tool-versions first (or versions, in a full run).
# ---------------------------------------------------------------------------

# The tool prompts' fixed text. The MCP tool is the recon's own stub's echo;
# the URL is the IANA's example domain; the path is outside the recon folder,
# and holds only the list of the system's shells.
MCP_TEXT="hello from the recon"
PROMPT_MCP="Use the echo tool of the recon MCP server with the text \"$MCP_TEXT\". Do nothing else."
MCP_LONG_TEXT="The quick brown fox jumps over the lazy dog. The quick brown fox jumps over the lazy dog. The quick brown fox jumps over the lazy dog. The quick brown fox jumps over the lazy dog."
PROMPT_MCP_LONG="Use the echo tool of the recon MCP server with exactly this text, nothing added: $MCP_LONG_TEXT Do nothing else."
PROMPT_MCP_DESC="Use the note tool of the recon MCP server with the text \"$MCP_TEXT\". Do nothing else."
FETCH_URL="https://example.com/"
PROMPT_FETCH="Use the WebFetch tool to fetch $FETCH_URL and tell me its title. Do nothing else."
FETCH_LONG_URL="https://example.com/?q=the-quick-brown-fox-jumps-over-the-lazy-dog-the-quick-brown-fox-jumps-over-the-lazy-dog-the-quick-brown-fox"
PROMPT_FETCH_LONG="Use the WebFetch tool to fetch exactly this URL, nothing changed: $FETCH_LONG_URL Do nothing else."
READ_PATH="/etc/shells"
PROMPT_READ="Use the Read tool to read the file $READ_PATH. Do nothing else."

# screen_box: the detection text below its last line drawn only with ─, as
# keymap_box cuts a capture.
screen_box() { detection_text | keymap_box -; }

# expect_box <name> <title ERE> <ERE>: the agent is blocked, its prompt box's
# title line matches <title ERE> (TITLE_*: anchored, so a Bash command the
# agent falls back to, `curl …` or `cat /etc/shells`, is never saved as a
# tool prompt; O-88's review), and the box matches <ERE>. Otherwise it saves
# the screen as <name>-unexpected, sends esc, and returns non-zero.
expect_box() {
  local name="$1" title="$2" re="$3" st box
  st="$(status_now)"
  sleep 1
  box="$(screen_box)"
  if [[ "$st" == "blocked" ]] && head -1 <<<"$box" | grep -qE -- "$title" && grep -qE -- "$re" <<<"$box"; then return 0; fi
  echo "$name: not the expected prompt (the agent is $st); saving the screen as $name-unexpected and sending esc"
  capture_stem "$name-unexpected" || true
  send_keys esc
  return 4
}

# deny_tool <name>: deny the prompt on screen with esc, and wait for the turn
# to end. Never an approve.
deny_tool() {
  local st
  send_keys esc
  st="$(wait_status 30000 idle done)"
  echo "$1: after esc, the agent is $st"
}

# tool_prompt <name> <prompt> <title ERE> <ERE> <once|three>: send the
# prompt, check the box's title and the box, capture it (three times, or
# once), and deny it.
tool_prompt() {
  local name="$1" prompt="$2" title="$3" re="$4" n="$5"
  assert_isolated
  CAPTURE_DIR="$TOOL_DIR"
  [[ -s "$TOOL_DIR/VERSIONS" ]] || die "$name: no tool-prompts/VERSIONS: run tool-versions first"
  local st; st="$(status_now)"
  [[ "$st" == "idle" || "$st" == "done" ]] || die "$name: the agent is $st, not idle"
  send_prompt "$prompt"
  expect_box "$name" "$title" "$re" || return $?
  if [[ "$n" == "three" ]]; then capture_three "$name"; else capture_stem "$name"; fi
  deny_tool "$name"
}

# show_redacted: stdin's lines, for a report: a URL becomes <URL>, a run of
# 24 or more letters, digits, - or _ becomes <TOKEN>, and a line holding a
# private value (privacy_needles) is withheld.
show_redacted() {
  local needles line
  needles="$(mktemp "$STATE_DIR/needles.XXXXXX")"
  privacy_needles | grep -v '^$' > "$needles" || true
  while IFS= read -r line; do
    line="$(sed -E -e 's#[A-Za-z][A-Za-z0-9+.-]*://[^[:space:]]+#<URL>#g' -e 's/[A-Za-z0-9_-]{24,}/<TOKEN>/g' <<<"$line")"
    if [[ -s "$needles" ]] && grep -qiF -f "$needles" <<<"$line"; then line="<a line withheld: it holds a private value>"; fi
    printf '  | %s\n' "$line"
  done
  rm -f "$needles"
}

# input_box_ready: stdin, a screen, ends with Claude Code's empty input box: a
# ─ rule, a ❯ line holding nothing or its placeholder (Try "…"), and a ─ rule,
# with only the footer below, and no numbered option line in its last 12
# lines (O-90's review: no dialog is open).
input_box_ready() {
  # The placeholder follows the ❯ after a no-break space (U+00A0).
  awk -v nb="$(printf '\302\240')" '
    { l[NR] = $0 }
    END {
      for (i = 1; i <= NR; i++) {
        s = l[i]
        if (!sub(/^❯/, "", s)) continue
        while (substr(s, 1, 1) == " " || (length(nb) > 0 && index(s, nb) == 1)) s = substr(s, substr(s, 1, 1) == " " ? 2 : length(nb) + 1)
        if ((s == "" || s ~ /^Try ".*"$/) && l[i-1] ~ /^(─)+$/ && l[i+1] ~ /^(─)+$/ && NR - (i + 1) <= 3) box = 1
      }
      for (i = (NR > 12 ? NR - 11 : 1); i <= NR; i++) if (l[i] ~ /^ *(❯ *)?[0-9]+\. /) options = 1
      exit !(box && !options)
    }'
}

# chrome_screen: stdin, a screen, is Claude Code's "Claude in Chrome
# extension detected" screen (DETECTION.md §The MCP prompt), and nothing
# else: its header, its two options with "No, keep browser tools off", and
# "Esc to keep browser tools off", in its own dialog (no other ─ rule
# below the header), and none of the words of a sign-in or an auth error.
# Only then does onboard answer it, with esc (the owner's decision,
# 2026-11-02).
chrome_screen() {
  local s; s="$(cat)"
  grep -qx '  Claude in Chrome extension detected' <<<"$s" &&
    grep -qE '^  (❯ |  )No, keep browser tools off$' <<<"$s" &&
    grep -qE '^  (❯ |  )Yes, use my browser$' <<<"$s" &&
    grep -qx '  Enter to confirm · Esc to keep browser tools off' <<<"$s" &&
    ! awk '/^  Claude in Chrome extension detected$/ { h = 1; next } h && /^(─)+$/ { r = 1 } END { exit !r }' <<<"$s" &&
    ! grep -qiE 'log ?in|sign ?in|api key|authenticat|oauth|subscription|console account|credential|unauthori[sz]ed' <<<"$s"
}

# onboard: the isolated session's first-run screens (O-90). It passes a theme
# or "press enter" screen with enter, and accepts the folder-trust dialog
# for the recon's own folder only; it stops, capturing nothing, at anything
# that asks to sign in, names an API key or an auth error, or wants a
# browser, at any screen that asks to choose a model or a plan, or to accept
# terms or a policy (it accepts nothing for the owner), and at any screen it
# doesn't know, printing what it shows (redacted). It sends fixed keys only,
# and no message.
cmd_onboard() {
  assert_isolated
  session_isolated || die "onboard: only in a session started with start-isolated"
  local i screen st chrome=0
  for i in 1 2 3 4 5 6 7 8; do
    within_limits
    sleep 3
    screen="$(detection_text)"
    st="$(status_now)"
    # The Chrome extension screen (the owner's decision, 2026-11-02): esc,
    # its own "keep browser tools off", and nothing else; once only, and
    # the next screen must no longer show it.
    if chrome_screen <<<"$screen"; then
      if [[ $chrome == 1 ]]; then
        echo "onboard: STOP: the Chrome extension screen is still there after esc; redacted:"
        tail -n 30 <<<"$screen" | show_redacted
        return 7
      fi
      send_keys esc
      chrome=1
      echo "onboard: declined the Claude in Chrome extension screen with esc (keep browser tools off)"
      continue
    fi
    if [[ $chrome == 1 ]] && grep -qF 'Claude in Chrome extension detected' <<<"$screen"; then
      echo "onboard: STOP: the screen still shows the Chrome extension's header after esc; redacted:"
      tail -n 30 <<<"$screen" | show_redacted
      return 7
    fi
    if grep -qiE 'log ?in|sign ?in|api key|authenticat|oauth|subscription|console account|credential|unauthori[sz]ed|browser' <<<"$screen"; then
      echo "onboard: STOP: the nested Claude Code asks to sign in, or shows an auth error (agent $st); its screen, redacted:"
      tail -n 30 <<<"$screen" | show_redacted
      return 3
    fi
    # Ready: the input box, in manual mode. herdr says done rather than idle
    # once a dialog has ended with the shell pane focused (DETECTION.md).
    if [[ "$st" == "idle" || "$st" == "done" ]] && footer_has "manual mode on" && input_box_ready <<<"$screen"; then
      echo "onboard: ready: at the input box, in manual mode (agent $st)"
      return 0
    fi
    if grep -qE 'trust this folder|trust the files|Do you trust' <<<"$screen"; then
      grep -qE '(/private)?/tmp/herdr-pager-recon([[:space:]]|$)' <<<"$screen" \
        || { echo "onboard: STOP: a trust dialog for another folder; its screen, redacted:"; tail -n 30 <<<"$screen" | show_redacted; return 4; }
      if ! selected_line | grep -qE 'Yes, I trust this folder|Yes, proceed'; then send_keys down; sleep 0.5; fi
      selected_line | grep -qE 'Yes, I trust this folder|Yes, proceed' || { echo "onboard: STOP: could not select the trust option"; return 4; }
      send_keys enter
      echo "onboard: accepted the trust dialog for the recon's own folder"
      continue
    fi
    # Not on the launch line, which names --model.
    if grep -vF -e '--setting-sources' -e '--permission-mode' <<<"$screen" \
        | grep -qiE 'terms|polic(y|ies)|agree|accept|consent|model|plan|upgrade|billing|pric(e|ing)'; then
      echo "onboard: STOP: a screen that asks to choose a model or a plan, or to accept terms or a policy (agent $st); it accepts nothing for the owner; redacted:"
      tail -n 30 <<<"$screen" | show_redacted
      return 6
    fi
    if grep -qiE 'text style|theme|dark mode|light mode|press enter to continue' <<<"$screen"; then
      send_keys enter
      echo "onboard: passed a first-run screen with enter: $(grep -m1 -iE 'text style|theme|dark mode|light mode|press enter to continue' <<<"$screen" | sed 's/^ *//' | cut -c1-80)"
      continue
    fi
    if [[ "$(grep -c '[^[:space:]]' <<<"$screen" || true)" -lt 3 ]]; then
      echo "onboard: the screen is still nearly empty; waiting"
      continue
    fi
    echo "onboard: STOP: a screen it doesn't know (agent $st); redacted:"
    tail -n 30 <<<"$screen" | show_redacted
    return 5
  done
  echo "onboard: STOP: still not at the input box after 8 screens"
  return 5
}

# mcp, mcp-long: a call of the stub's echo tool, x3, and one whose argument
# wraps, x1; denied. The stub must have logged no call. Only in a session
# started with start-isolated (O-90): safe mode loads no MCP server.
mcp_session() {
  session_isolated || die "mcp: safe mode loads no MCP server: run start-isolated (and onboard) first"
  ALLOW_ISOLATED=1
}
cmd_mcp() {
  mcp_session
  local before; before="$(mcp_calls)"
  tool_prompt mcp "$PROMPT_MCP" "$TITLE_MCP" '^ recon .*\(MCP\)$' three || return $?
  echo "mcp: tool calls the stub logged: before $before, after $(mcp_calls) (want 0, 0)"
  [[ "$(mcp_calls)" == "0" ]] || { echo "mcp: FAILED: the denied call ran"; return 6; }
}
cmd_mcp_long() {
  mcp_session
  tool_prompt mcp-long "$PROMPT_MCP_LONG" "$TITLE_MCP" '^ recon .*\(MCP\)$' once || return $?
  echo "mcp-long: tool calls the stub logged: $(mcp_calls) (want 0)"
  [[ "$(mcp_calls)" == "0" ]] || { echo "mcp-long: FAILED: the denied call ran"; return 6; }
}

# mcp-desc: a call of the stub's note tool, whose description runs over several
# lines, with blank lines and markdown, x1, denied (O-90's review: how Claude
# Code draws a server's text between the last rule and the question).
cmd_mcp_desc() {
  mcp_session
  tool_prompt mcp-desc "$PROMPT_MCP_DESC" "$TITLE_MCP" '^ recon .*\(MCP\)$' once || return $?
  echo "mcp-desc: tool calls the stub logged: $(mcp_calls) (want 0)"
  [[ "$(mcp_calls)" == "0" ]] || { echo "mcp-desc: FAILED: the denied call ran"; return 6; }
}

# webfetch, webfetch-long: a fetch of the example domain, x3, and one whose
# URL is longer than a line, x1; denied.
cmd_webfetch() { tool_prompt webfetch "$PROMPT_FETCH" "$TITLE_FETCH" 'example\.com' three; }
cmd_webfetch_long() { tool_prompt webfetch-long "$PROMPT_FETCH_LONG" "$TITLE_FETCH" 'example\.com' once; }

# read-outside: a read of a file outside the recon folder, x3, denied.
cmd_read_outside() { tool_prompt read-outside "$PROMPT_READ" "$TITLE_READ" '/etc/shells' three; }

# focus-check: 4.8 (R3.14). Once the last task has finished and the agent shows
# done, focus its pane and check done becomes idle. Isolated session only.
cmd_focus_check() {
  assert_isolated
  local st
  st="$(status_now)"
  if [[ "$st" != "done" ]]; then
    send_prompt "$PROMPT_FINISH"
    st="$(wait_status 60000 done idle)"
  fi
  echo "focus-check: before focusing, the agent is $st; agent pane focused: $(agent_focused)"
  [[ "$st" == "done" ]] || { echo "focus-check: FAILED: the agent is $st, not done"; return 5; }
  capture_list before-focus
  herdr_call agent focus "$AGENT" >/dev/null
  st="$(wait_status 15000 idle)"
  echo "focus-check: after agent focus, the agent is $st; agent pane focused: $(agent_focused)"
  capture_list after-focus
  [[ "$st" == "idle" ]]
}

# tokens: herdr's token count for the recon's agent (R3.6).
cmd_tokens() {
  assert_isolated
  echo "tokens (agent get): $(herdr_call agent get "$AGENT" | jq -c '[.. | objects | select(has("agent_status"))][0] | {tokens: (.tokens // null), usage: (.usage // null)}')"
  echo "messages sent: $(state_get messages) (including any carried from an earlier run)"
}

# summary: for DETECTION.md, herdr's state and rule for each detection capture,
# the agent's state_labels, and whether agent_session is present. Offline.
cmd_summary() {
  local f stem g
  echo "| capture | herdr state | rule | state_labels | agent_session present |"
  echo "|---|---|---|---|---|"
  for f in "$CLAUDE_DIR"/explain/*.json "$TOOL_DIR"/explain/*.json; do
    [[ -e "$f" ]] || continue
    stem="$(basename "$f" .json)"; g="$(dirname "$(dirname "$f")")/get/$stem.json"
    [[ "$f" == "$TOOL_DIR"/* ]] && stem="tool-prompts/$stem"
    printf '| `%s` | %s | `%s` | %s | %s |\n' "$stem" \
      "$(jq -r '.state // "?"' "$f")" "$(jq -r '.matched_rule.id // "none"' "$f")" \
      "$(jq -c '[.. | objects | select(has("agent_status"))][0].state_labels // null' "$g")" \
      "$(jq -r '[.. | objects | select(has("agent_status"))][0] | has("agent_session")' "$g")"
  done
  echo
  for f in "$CLAUDE_DIR"/list/*.json; do
    printf -- '- `list/%s`: %s\n' "$(basename "$f")" \
      "$(jq -c '.result.agents[0] | {agent, agent_status, state_change_seq, focused, tokens: (.tokens // null), agent_session: has("agent_session")}' "$f")"
  done
}

# cleanup: stop and delete the recon's named session, and delete the recon's
# own folders (R3.3, R3.5). Acts only on the fixed names this script created.
cmd_cleanup() {
  local st listed
  st="$(herdr_call status server 2>&1 || true)"
  if grep -q 'status: running' <<<"$st"; then
    assert_isolated
    herdr_call session stop "$SESSION" >/dev/null 2>&1 || true
    local i
    for i in $(seq 1 20); do
      sleep 0.5
      st="$(herdr_call status server 2>&1 || true)"
      grep -q 'status: running' <<<"$st" || break
    done
  fi
  herdr_call session delete "$SESSION" >/dev/null 2>&1 || true
  rm -rf -- "$RECON_DIR" "$STATE_DIR"

  listed="$(herdr_call session list --json 2>/dev/null | jq -r '[.. | strings | select(. == "'"$SESSION"'")] | length' 2>/dev/null || echo "?")"
  echo "cleanup: session listed: $listed (want 0)"
  echo "cleanup: server: $(grep -o 'status: [a-z ]*' <<<"$st" | head -1)"
  [[ -e "$RECON_DIR" ]] && echo "cleanup: $RECON_DIR still exists" || echo "cleanup: $RECON_DIR gone"
  [[ -e "$STATE_DIR" ]] && echo "cleanup: $STATE_DIR still exists" || echo "cleanup: $STATE_DIR gone"
  [[ "$listed" == "0" && ! -e "$RECON_DIR" && ! -e "$STATE_DIR" ]]
}

# screen [n]: the status and the last n lines of the detection text (default 25).
cmd_screen() {
  assert_isolated
  echo "status: $(status_now)"
  detection_text | tail -n "${1:-25}"
}

# stop-evidence: save the screen and herdr's view when a stop rule fires.
cmd_stop_evidence() {
  assert_isolated
  case "${1:-}" in
    touch-no-prompt|no-done|no-question|no-working) ;;
    *) die "unknown evidence name" ;;
  esac
  capture_once "$1" now
  capture_list "after-$1"
}

# peek: the shape of herdr's outputs for the recon's agent (keys only, no text).
cmd_peek() {
  assert_isolated
  local p; p="$(pane_id)"
  echo "-- agent list (keys):"; herdr_call agent list | jq -c 'paths(scalars) | map(tostring) | join(".")' | sed 's/\.[0-9]*\././' | sort -u | head -60 | tr '\n' ' '; echo
  echo "-- agent get $AGENT: status"; herdr_call agent get "$AGENT" | jq -c '[.. | objects | select(has("agent_status")) | {agent, agent_status, state_labels, pane_id, agent_session}][0]'
  local ex; ex="$(herdr_call agent explain "$AGENT" --json)"
  echo "-- explain: where the owner's login or home appears (path, then the string with them masked):"
  jq -c --arg u "$(id -un)" --arg h "$HOME" 'paths(strings) as $p | getpath($p) as $s
      | select(($s | ascii_downcase | contains($u | ascii_downcase)) or ($s | contains("/Users/")))
      | [($p | map(tostring) | join(".")), ($s | gsub($h; "<HOME>") | gsub("(?i)" + $u; "<LOGIN>") | .[0:200])]' <<<"$ex" | head -12
  echo "-- explain top-level keys:"; jq -c 'keys' <<<"$ex"
  echo "-- explain, without rules:"; jq -c 'del(.evaluated_rules) | walk(if type == "string" and (test("/Users/|/var/folders/")) then "<path>" else . end)' <<<"$ex" | cut -c1-900
  echo "-- explain rules:"; jq -c '.evaluated_rules[] | {id, state, priority, region, matched: (.matched // .evidence.matched // null)}' <<<"$ex" | head -30
  echo "-- detection text (first 40 lines):"; herdr_call agent read "$AGENT" --source detection --format text | head -40
}

# claude-flags: which of the clean-settings flags (R3.5) this Claude Code has.
cmd_claude_flags() {
  local help f
  help="$(claude --help 2>&1 || true)"
  for f in --model --safe-mode --setting-sources --strict-mcp-config --permission-mode \
           --bare --mcp-config --settings --disable-slash-commands --no-hooks; do
    if grep -q -- "$f" <<<"$help"; then
      echo "has $f: $(grep -m1 -- "$f" <<<"$help" | sed 's/^ *//' | cut -c1-160)"
    else
      echo "lacks $f"
    fi
  done
  echo "permission-mode text:"; grep -A4 -- '--permission-mode <' <<<"$help" | tr -s ' ' | tr '\n' ' '; echo
  echo "safe-mode text:"; grep -A3 -- '--safe-mode' <<<"$help" | tr -s ' ' | tr '\n' ' '; echo
}

usage() {
  cat <<'EOF'
usage: capture-claude.sh <subcommand>
  check [dir] [keymap]   offline fixture check (completeness, JSON, privacy, keymap)
  selftest               check fails on an empty dir and on a planted /Users/ path
  help-topic <topic>     herdr's help ("" agent session server workspace pane api skill,
                         or <group>.<command> from a fixed list)
  claude-flags           which clean-settings flags (R3.5) this Claude Code has
  versions               herdr's version and protocol, Claude Code's version (R3.2)
  server-up              start the recon's own headless named session (R3.3)
  start [messages-used]  the recon folder, the agent's pane, a focused shell pane,
                         and Claude Code with clean settings and no auto-update
  start-isolated [n]     O-90: the same, for mcp alone: no --safe-mode, and the
                         recon's own sign-in as CLAUDE_CONFIG_DIR
                         (~/.herdr-pager-recon/claude, 0700; the owner signs in
                         there once by hand)
  onboard                O-90: pass the isolated session's first-run screens; stop
                         at any sign-in or auth screen
  trust                  capture the folder-trust prompt x3, accept it, capture idle
  shell | shell-again    4.4: touch asks permission, captures, approve, done, repeat
  working                4.5: a long command, approved; working; interrupt (esc)
  question               4.6: a two-option question x3, blocked list, answered
  file-edit | file-create  4.6: the edit and create prompts x3, denied (esc)
  plan                   4.6: the plan-approval prompt x3, declined (esc)
  danger                 4.7: git push --force to a throwaway bare repo, denied
  tool-versions          O-88: clear the tool prompts' captures; record the versions
  mcp | mcp-long | mcp-desc  O-88: the stub MCP server's echo x3, a wrapping one x1,
                         its note (a description over several lines) x1, denied
                         (safe mode loads no MCP server: DETECTION.md)
  webfetch | webfetch-long  O-88: WebFetch of the example domain x3, a long URL x1, denied
  read-outside           O-88: Read of a file outside the recon folder x3, denied
  focus-check            4.8: done, then focus the agent's pane: done -> idle
  tokens                 herdr's token count for the recon's agent
  summary                offline: herdr's state and rule per capture (DETECTION.md)
  screen [n]             status and the last n lines of the detection text
  peek                   the shape of herdr's outputs (keys only)
  stop-evidence <name>   save the screen when a stop rule fires
  cleanup                stop and delete the session; delete the recon's folders
EOF
}

main() {
  local sub="${1:-}"; shift || true
  case "$sub" in
    check)      cmd_check "$@" ;;
    selftest)   cmd_selftest ;;
    help-topic) cmd_help_topic "$@" ;;
    claude-flags) cmd_claude_flags ;;
    versions)   cmd_versions ;;
    server-up)  cmd_server_up ;;
    start)      cmd_start "$@" ;;
    start-isolated) cmd_start_isolated "$@" ;;
    onboard)    cmd_onboard ;;
    idle)       cmd_idle ;;
    working)    cmd_working ;;
    question)   cmd_question ;;
    file-edit)  cmd_file_edit ;;
    file-create) cmd_file_create ;;
    plan)       cmd_plan ;;
    danger)     cmd_danger ;;
    tool-versions) cmd_tool_versions ;;
    mcp)        cmd_mcp ;;
    mcp-long)   cmd_mcp_long ;;
    mcp-desc)   cmd_mcp_desc ;;
    webfetch)   cmd_webfetch ;;
    webfetch-long) cmd_webfetch_long ;;
    read-outside) cmd_read_outside ;;
    focus-check) cmd_focus_check ;;
    tokens)     cmd_tokens ;;
    summary)    cmd_summary ;;
    diffs)      cmd_diffs ;;
    peek)       cmd_peek ;;
    screen)     cmd_screen "$@" ;;
    stop-evidence) cmd_stop_evidence "$@" ;;
    cleanup)    cmd_cleanup ;;
    trust)      cmd_trust ;;
    shell)      cmd_shell ;;
    shell-again) cmd_shell_again ;;
    ""|-h|--help) usage ;;
    *) usage; exit 2 ;;
  esac
}

main "$@"
