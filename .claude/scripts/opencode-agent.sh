#!/usr/bin/env bash
# Run one opencode agent on DeepSeek V4.1 Flash (OpenCode Go subscription),
# then PROVE which model ran and that it only touched the files it may touch.
#
# Usage:
#   opencode-agent.sh --role <name> --prompt-file <file> --log-dir <dir> \
#                     [--allow <repo-relative-path>]... [--timeout <seconds>]
#
# --allow   a repo file the agent is permitted to change (repeatable).
#           With no --allow the agent must not change anything in the repo
#           (code-search agents write their notes outside the repo).
# --ignore  a repo file that ANOTHER agent may be editing at the same time
#           (e.g. docs/PROJECT_GUIDE.md while a writer runs); changes to it are not
#           blamed on this agent. Use sparingly: it weakens the guard for that file.
# Exit codes: 0 ok | 1 opencode failed | 2 usage | 3 wrong model | 4 changed a file it may not
set -euo pipefail

MODEL="opencode-go/deepseek-v4.1-flash"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo="$(git -C "$here" rev-parse --show-toplevel)"

role="" prompt_file="" log_dir="" timeout_s=900
allow=()
ignore=()
while [ $# -gt 0 ]; do
  case "$1" in
    --role) role="$2"; shift 2 ;;
    --prompt-file) prompt_file="$2"; shift 2 ;;
    --log-dir) log_dir="$2"; shift 2 ;;
    --allow) allow+=("$2"); shift 2 ;;
    --ignore) ignore+=("$2"); shift 2 ;;
    --timeout) timeout_s="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
[ -n "$role" ] && [ -r "$prompt_file" ] && [ -n "$log_dir" ] || { echo "usage: $0 --role R --prompt-file F --log-dir D [--allow PATH]..." >&2; exit 2; }
command -v opencode >/dev/null || { echo "opencode not found" >&2; exit 2; }
mkdir -p "$log_dir"

# Fingerprint every modified/untracked file in the repo (path + content hash).
snapshot() {
  git -C "$repo" status --porcelain -uall | sed -E 's/^.{3}//' | sort | while IFS= read -r f; do
    if [ -f "$repo/$f" ]; then printf '%s %s\n' "$(shasum "$repo/$f" | cut -d' ' -f1)" "$f"; else printf 'DELETED %s\n' "$f"; fi
  done
}

ts="$(date +%Y%m%d-%H%M%S)"
log="$log_dir/$role-$ts.jsonl"
before="$(snapshot)"

# --auto approves permission prompts (needed: headless run, notes live outside the repo).
# The model check and the file guard below are what keep that safe.
set +e
# </dev/null is REQUIRED: `opencode run` waits on an open stdin and hangs forever.
# OPENCODE_DISABLE_EXTERNAL_SKILLS=1 mirrors the user's interactive `opencode` shell
# function for this repo (a script does not get that function).
OPENCODE_DISABLE_EXTERNAL_SKILLS=1 perl -e 'alarm shift; exec @ARGV' "$timeout_s" \
  opencode run -m "$MODEL" --dir "$repo" --format json --auto "$(cat "$prompt_file")" </dev/null >"$log" 2>"$log.err"
rc=$?
set -e
if [ $rc -ne 0 ] || ! grep -q '"type":"step_finish"' "$log"; then
  echo "FAIL $role: opencode exited $rc or never finished (see $log and $log.err)"; exit 1
fi

# grep -m1 stops at the first match: `grep | head -1` on a big log dies of SIGPIPE under pipefail.
sid="$(grep -m1 -o '"sessionID":"ses_[A-Za-z0-9]*"' "$log" | head -1 | cut -d'"' -f4 || true)"
[ -n "$sid" ] || { echo "FAIL $role: no session id in $log"; exit 1; }

# 1. Prove the model.
"$here/check-agent-model.sh" --opencode "$sid" "$MODEL" || { echo "FAIL $role: wrong model"; exit 3; }

# 2. Prove it only changed allowed files.
after="$(snapshot)"
changed="$(diff <(printf '%s\n' "$before") <(printf '%s\n' "$after") | grep '^[<>]' | sed -E 's/^[<>] [^ ]+ //' | sort -u || true)"
bad=""
while IFS= read -r f; do
  [ -z "$f" ] && continue
  ok=0; for a in "${allow[@]:-}" "${ignore[@]:-}"; do [ -n "$a" ] && [ "$f" = "$a" ] && ok=1; done
  [ $ok -eq 1 ] || bad="$bad $f"
done <<<"$changed"
if [ -n "$bad" ]; then echo "FAIL $role: changed files it may not touch:$bad"; exit 4; fi

echo "DONE $role session=$sid log=$log"
