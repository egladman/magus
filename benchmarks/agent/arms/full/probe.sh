#!/usr/bin/env sh
# Prove a worktree is in ARM-1 "full". The surface must not merely be present
# but CURRENT for the binary this arm was handed, which is what doctor grades:
# a stale skill set or a stale guard template measures a dangling-reference
# surface rather than the shipped one.
set -eu

# shellcheck disable=SC2034  # read by lib.sh, which shellcheck does not follow without -x
ARM_NAME=full
# shellcheck source=../lib.sh disable=SC1091
. "$(dirname "$0")/../lib.sh"

WT=$(arm_worktree "${1:-}")
arm_load_env "$WT"

[ "${MAGUS_HINTS_ENABLED:-true}" = "true" ] || arm_die "hints are disabled (MAGUS_HINTS_ENABLED=$MAGUS_HINTS_ENABLED)"
[ "${GUARD_MAGUS_BIN:-}" = "$BENCH_MAGUS_BIN" ] || arm_die "hooks would run ${GUARD_MAGUS_BIN:-the binary they resolve themselves}, not $BENCH_MAGUS_BIN"

for t in magus-guard-command.sh magus-guard-path.sh magus-guard-observe.sh; do
    [ -f "$WT/.claude/hooks/$t" ] || arm_die "$t is not installed, so that hook runs nothing"
    if ! grep -q 'magus-guard-template:' "$WT/.claude/hooks/$t"; then
        arm_die "$t carries no magus-guard-template marker, so its version cannot be graded"
    fi
done

settings=$WT/.claude/settings.json
[ -f "$settings" ] || arm_die "no .claude/settings.json, so nothing invokes the guard"
wired=$(jq -r '[.hooks.PreToolUse[].hooks[] | select(.command | test("\\.claude/hooks/magus-guard-"))] | length' "$settings")
[ "$wired" = "3" ] || arm_die "$wired hook(s) point at the installed templates, expected 3"

[ -s "$WT/MAGUS.md" ] || arm_die "MAGUS.md is missing or empty, so there is no routing index"
if ! grep -q 'magus:skills:begin' "$WT/CLAUDE.md"; then
    arm_die "CLAUDE.md carries no magus:skills block"
fi

report=$(arm_doctor "$WT") || true
[ -n "$report" ] || arm_die "$BENCH_MAGUS_BIN doctor produced no JSON"

skills=$(printf '%s' "$report" | jq -r '.checks[] | select(.name == "agent-skills") | .status + ": " + .message')
case $skills in
ok:*) ;;
*) arm_die "doctor agent-skills says $skills" ;;
esac

expected=$(cat "$WT/.benchmark/skills.expected" 2>/dev/null || echo 0)
installed=$(find "$WT/.claude/skills" -name SKILL.md 2>/dev/null | wc -l | tr -d ' ')
[ "$expected" -gt 0 ] || arm_die "provisioning recorded no installed skills"
[ "$installed" = "$expected" ] || arm_die "$installed skill file(s) installed, $expected at provisioning time"

guard=$(printf '%s' "$report" | jq -r '.checks[] | select(.name == "guard-wiring") | .status + ": " + .message + " " + (.details // [] | join("; "))')
case $guard in
ok:*"$settings"*) ;;
*) arm_die "doctor guard-wiring says $guard" ;;
esac

# MCP is held constant across the arms rather than switched, so its absence is
# an assertion here too and not only in the rampant probe.
arm_check_no_mcp "$WT"

printf 'arm full: %s carries the current agent surface\n' "$WT"
