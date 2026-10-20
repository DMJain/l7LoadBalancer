#!/usr/bin/env bash
# Verify which model actually executed a sub-agent, from its transcript file.
# Usage: check-agent-model.sh <agent-output-file>... [-- expected-model]
#        check-agent-model.sh --opencode <session-id> [expected provider/model]
# Default expected model: claude-haiku-5-5. Exit 1 if any other model ran.
# --opencode mode reads `opencode export <session-id>` and checks every
# providerID/modelID pair (default: opencode-go/deepseek-v4.1-flash).
# Reads the "model" fields of the transcript; ignores the advisor tool config
# ("name":"advisor","model":...) and advisorModel, which are not the executing model.
set -euo pipefail

if [ "${1:-}" = "--opencode" ]; then
  sid="${2:?session id missing}"; want="${3:-opencode-go/deepseek-v4.1-flash}"
  # opencode export TRUNCATES at 64-128 KB when stdout is a pipe; write to a file instead.
  tmp="$(mktemp -t ocexport.XXXXXX)"; trap 'rm -f "$tmp"' EXIT
  (cd /tmp && opencode export "$sid" >"$tmp" 2>/dev/null) || { echo "FAIL opencode export $sid failed"; exit 1; }
  python3 -I -c '
import json, sys, collections
sid, want = sys.argv[1], sys.argv[2]
raw = open(sys.argv[3]).read()
try:
    data = json.loads(raw[raw.index("{"):])
except Exception as e:
    print(f"FAIL opencode {sid}: cannot parse export ({e})"); sys.exit(1)
pairs = collections.Counter()
def walk(x):
    if isinstance(x, dict):
        if "providerID" in x and "modelID" in x:
            pairs[x["providerID"] + "/" + x["modelID"]] += 1
        for v in x.values(): walk(v)
    elif isinstance(x, list):
        for v in x: walk(v)
walk(data)
if not pairs:
    print(f"FAIL opencode {sid}: no provider/model found"); sys.exit(1)
summary = ", ".join(f"{n} x {m}" for m, n in pairs.items())
if set(pairs) != {want}:
    print(f"FAIL opencode {sid}: not only {want}: {summary}"); sys.exit(1)
print(f"OK   opencode {sid}: {summary}")
' "$sid" "$want" "$tmp"
  exit $?
fi

expected="claude-haiku-5-5"
files=()
while [ $# -gt 0 ]; do
  if [ "$1" = "--" ]; then expected="${2:?expected model missing}"; break; fi
  files+=("$1"); shift
done
[ ${#files[@]} -gt 0 ] || { echo "usage: $0 <agent-output-file>... [-- model]" >&2; exit 2; }

status=0
for f in "${files[@]}"; do
  [ -r "$f" ] || { echo "FAIL $f: unreadable" >&2; status=1; continue; }
  models=$(sed 's/"name":"advisor","model":"claude-[^"]*"//g' "$f" | grep -o '"model":"claude-[^"]*"' | sort | uniq -c || true)
  if [ -z "$models" ]; then
    echo "FAIL $f: no model entries found"; status=1; continue
  fi
  other=$(printf '%s\n' "$models" | grep -v "\"$expected\"" || true)
  if [ -n "$other" ]; then
    echo "FAIL $f: ran on a model other than $expected"; printf '%s\n' "$models"; status=1
  else
    echo "OK   $f: $(printf '%s' "$models" | tr -s ' ')"
  fi
done
exit $status
