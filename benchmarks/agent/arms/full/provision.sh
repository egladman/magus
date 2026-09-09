#!/usr/bin/env sh
# Provision ARM-1 "full": the installed agent surface - skills, guard hooks,
# the MAGUS.md routing index, the CLAUDE.md magus block, and hints. MCP is NOT
# switched on; see ../README.md for that decision and the switch table.
set -eu

# shellcheck disable=SC2034  # read by lib.sh, which shellcheck does not follow without -x
ARM_NAME=full
# shellcheck source=../lib.sh disable=SC1091
. "$(dirname "$0")/../lib.sh"

WT=$(arm_worktree "${1:-}")
BIN=$(arm_binary "${2:-}")
ARMS=$(cd "$(dirname "$0")/.." && pwd)
TEMPLATES=$(arm_templates)

arm_reset "$WT"

# GUARD_MAGUS_BIN is what pins the hooks to the binary this arm was handed. The
# templates otherwise walk up to the magusfile and fall back to PATH, which
# would let a magus installed on the benchmark machine judge the run.
arm_write_env "$WT" "$BIN" <<ENV
GUARD_MAGUS_BIN="$BIN"; export GUARD_MAGUS_BIN
ENV

# Hints are part of the arm, not of provisioning it: install's own hint block is
# a page of advice about installing, addressed to nobody here.
MAGUS_HINTS_ENABLED=false "$BIN" agent install --dir "$WT" .claude/skills --force --prune -q

# Recorded because doctor grades the skills it FINDS: it reads one anchor skill
# to decide the set is installed, so a skill that goes missing between
# provisioning and the run is invisible to it. The probe re-counts.
find "$WT/.claude/skills" -name SKILL.md | wc -l | tr -d ' ' > "$WT/.benchmark/skills.expected"

mkdir -p "$WT/.claude/hooks"
for t in magus-guard-command.sh magus-guard-path.sh magus-guard-observe.sh; do
    cp "$TEMPLATES/$t" "$WT/.claude/hooks/$t"
done

cat > "$WT/.claude/settings.json" <<'JSON'
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "sh .claude/hooks/magus-guard-command.sh", "timeout": 10 }]
      },
      {
        "matcher": "Edit|Write|NotebookEdit",
        "hooks": [{ "type": "command", "command": "sh .claude/hooks/magus-guard-path.sh", "timeout": 10 }]
      },
      {
        "matcher": "Read",
        "hooks": [{ "type": "command", "command": "sh .claude/hooks/magus-guard-observe.sh", "timeout": 10 }]
      }
    ]
  }
}
JSON

index=$("$BIN" --root "$WT" describe graph -o markdown -s)
printf '%s\n' "$index" > "$WT/MAGUS.md"

# The magus block is generated and stamped, so it is taken from the binary
# rather than copied into this tree, where it would go stale silently.
block=$("$BIN" --root "$WT" agent sample | sed -n '/magus:skills:begin/,/magus:skills:end/p')
[ -n "$block" ] || arm_die "magus agent sample printed no magus:skills block"
cat "$ARMS/repo-description.md" > "$WT/CLAUDE.md"
printf '\n%s\n' "$block" >> "$WT/CLAUDE.md"

printf 'arm full: provisioned %s (magus %s)\n' "$WT" "$BIN"
