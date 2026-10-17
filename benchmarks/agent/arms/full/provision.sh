#!/usr/bin/env sh
# Provision ARM-1 "full": the installed agent integration - skills, guard hooks,
# the MAGUS.md routing index, the CLAUDE.md magus block, and hints. MCP is NOT
# switched on; see ../README.md for that decision and the switch table.
#
# BENCH_SKILL_FORM picks the installed skill form (both, short or full; default
# both). BENCH_SKILLS_DIR, when set, is a skills directory copied in verbatim
# instead of installing; the skills-* arms set these and reuse this recipe.
set -eu

# shellcheck disable=SC2034  # read by lib.sh, which shellcheck does not follow without -x
ARM_NAME=${ARM_NAME:-full}
# shellcheck source=../lib.sh disable=SC1091
. "$(dirname "$0")/../lib.sh"

WT=$(arm_worktree "${1:-}")
BIN=$(arm_binary "${2:-}")
ARMS=$(cd "$(dirname "$0")/.." && pwd)
TEMPLATES=$(arm_templates)

arm_reset "$WT"

# __MAGUS_BIN is what pins the hooks to the binary this arm was handed. The
# templates otherwise walk up to the magusfile and fall back to PATH, which
# would let a magus installed on the benchmark machine judge the run.
arm_write_env "$WT" "$BIN" <<ENV
__MAGUS_BIN="$BIN"; export __MAGUS_BIN
ENV

if [ -n "${BENCH_SKILLS_DIR:-}" ]; then
    [ -d "$BENCH_SKILLS_DIR" ] || arm_die "BENCH_SKILLS_DIR=$BENCH_SKILLS_DIR is not a directory"
    src=$(cd "$BENCH_SKILLS_DIR" && pwd)
    mkdir -p "$WT/.claude/skills"
    cp -R "$src/." "$WT/.claude/skills/"
    printf 'dir %s\n' "$src" > "$WT/.benchmark/skills.source"
else
    # Hints are part of the arm, not of provisioning it: install's own hint block is
    # a page of advice about installing, addressed to nobody here.
    form=${BENCH_SKILL_FORM:-both}
    MAGUS_HINTS_ENABLED=false "$BIN" agent install --dir "$WT" .claude/skills --force --skill-form "$form" -q
    printf 'form %s\n' "$form" > "$WT/.benchmark/skills.source"
fi

# The Buzz templates import lib/hook.buzz relative to themselves, so it travels with them.
mkdir -p "$WT/.claude/hooks/lib"
for t in magus-command.buzz magus-path.buzz magus-observe.buzz lib/hook.buzz; do
    cp "$TEMPLATES/$t" "$WT/.claude/hooks/$t"
done

cat > "$WT/.claude/settings.json" <<'JSON'
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "magus buzz -s .claude/hooks/magus-command.buzz -- --agent-name claude-code", "timeout": 10 }]
      },
      {
        "matcher": "Edit|Write|NotebookEdit",
        "hooks": [{ "type": "command", "command": "magus buzz -s .claude/hooks/magus-path.buzz -- --agent-name claude-code", "timeout": 10 }]
      },
      {
        "matcher": "Read",
        "hooks": [{ "type": "command", "command": "magus buzz -s .claude/hooks/magus-observe.buzz -- --agent-name claude-code", "timeout": 10 }]
      }
    ]
  }
}
JSON

index=$("$BIN" --root "$WT" describe graph -o markdown -s)
printf '%s\n' "$index" > "$WT/MAGUS.md"

# The magus block is generated and stamped, so it is taken from the binary
# rather than copied into this tree, where it would go stale silently.
block=$("$BIN" --root "$WT" agent starter | sed -n '/magus:skills:begin/,/magus:skills:end/p')
[ -n "$block" ] || arm_die "magus agent starter printed no magus:skills block"
cat "$ARMS/repo-description.md" > "$WT/CLAUDE.md"
printf '\n%s\n' "$block" >> "$WT/CLAUDE.md"

printf 'arm %s: provisioned %s (magus %s, skills %s)\n' "$ARM_NAME" "$WT" "$BIN" "$(cat "$WT/.benchmark/skills.source")"
