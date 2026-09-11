#!/usr/bin/env sh
# Prove a worktree is in ARM-0 "rampant". Every surface item the full arm
# installs must be absent, and the run environment must still resolve magus.
set -eu

# shellcheck disable=SC2034  # read by lib.sh, which shellcheck does not follow without -x
ARM_NAME=rampant
# shellcheck source=../lib.sh disable=SC1091
. "$(dirname "$0")/../lib.sh"

WT=$(arm_worktree "${1:-}")
arm_load_env "$WT"

[ "${MAGUS_HINTS_ENABLED:-}" = "false" ] || arm_die "hints are not disabled (MAGUS_HINTS_ENABLED=${MAGUS_HINTS_ENABLED:-unset})"

[ ! -d "$WT/.claude/skills" ] || arm_die ".claude/skills exists, so agent skills are installed"
[ ! -d "$WT/.claude/hooks" ] || arm_die ".claude/hooks exists, so guard templates are installed"
[ ! -e "$WT/MAGUS.md" ] || arm_die "MAGUS.md exists, so the routing index is present"

settings=$WT/.claude/settings.json
[ -f "$settings" ] || arm_die "no .claude/settings.json, so the arms differ in file shape and not only in hook behavior"
entries=$(jq '.hooks.PreToolUse | length' "$settings")
[ "$entries" = "3" ] || arm_die "$entries PreToolUse entries, expected 3"
loud=$(jq -r '[.hooks.PreToolUse[].hooks[] | select(.command != "true")] | length' "$settings")
[ "$loud" = "0" ] || arm_die "$loud hook command(s) are not the silent \"true\""

if grep -qi magus "$settings"; then
    arm_die "settings.json names magus, so a hook can still reach the guard"
fi
if grep -qi magus "$WT/CLAUDE.md"; then
    arm_die "CLAUDE.md names magus, so it routes where this arm must not"
fi

arm_check_no_mcp "$WT"

printf 'arm rampant: %s carries no agent surface\n' "$WT"
