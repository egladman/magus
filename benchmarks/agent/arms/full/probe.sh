#!/usr/bin/env sh
# Prove a worktree is in ARM-1 "full". The agent integration must not merely be
# present but CURRENT for the binary this arm was handed: a stale skill set or a
# guard that cannot run measures a dangling-reference integration rather than
# the shipped one.
set -eu

# shellcheck disable=SC2034  # read by lib.sh, which shellcheck does not follow without -x
ARM_NAME=${ARM_NAME:-full}
# shellcheck source=../lib.sh disable=SC1091
. "$(dirname "$0")/../lib.sh"

WT=$(arm_worktree "${1:-}")
arm_load_env "$WT"

[ "${MAGUS_HINTS_ENABLED:-true}" = "true" ] || arm_die "hints are disabled (MAGUS_HINTS_ENABLED=$MAGUS_HINTS_ENABLED)"
[ "${__MAGUS_BIN:-}" = "$BENCH_MAGUS_BIN" ] || arm_die "hooks would run ${__MAGUS_BIN:-the binary they resolve themselves}, not $BENCH_MAGUS_BIN"

[ -f "$WT/.claude/hooks/lib/hook.buzz" ] || arm_die "lib/hook.buzz is not installed beside the templates, so every hook fails to load"
for t in magus-command.buzz magus-path.buzz magus-observe.buzz; do
    [ -f "$WT/.claude/hooks/$t" ] || arm_die "$t is not installed, so that hook runs nothing"
    if ! grep -q 'magus-guard-template:' "$WT/.claude/hooks/$t"; then
        arm_die "$t carries no magus-guard-template marker, so its version cannot be graded"
    fi
done

settings=$WT/.claude/settings.json
[ -f "$settings" ] || arm_die "no .claude/settings.json, so nothing invokes the guard"
wired=$(jq -r '[.hooks.PreToolUse[].hooks[] | select(.command | test("\\.claude/hooks/magus-(command|path|observe)\\.buzz"))] | length' "$settings")
[ "$wired" = "3" ] || arm_die "$wired hook(s) point at the installed templates, expected 3"

[ -s "$WT/MAGUS.md" ] || arm_die "MAGUS.md is missing or empty, so there is no routing index"
if ! grep -q 'magus:skills:begin' "$WT/CLAUDE.md"; then
    arm_die "CLAUDE.md carries no magus:skills block"
fi

source=$(cat "$WT/.benchmark/skills.source" 2>/dev/null) || arm_die "provisioning recorded no skills source"
if [ -n "${BENCH_SKILLS_DIR:-}" ]; then
    want="dir $(cd "$BENCH_SKILLS_DIR" && pwd)"
else
    want="form ${BENCH_SKILL_FORM:-both}"
fi
[ "$source" = "$want" ] || arm_die "skills came from '$source', but this arm wants '$want'"

# Doctor grades skills and guard wiring only through a harness wired in a root
# magusfile, which the fixture does not carry, so both are proven directly: the
# skills byte-equal to a fresh render by the pinned binary (or to the custom
# source), and the wired Bash hook answering an event.
[ -n "$(find "$WT/.claude/skills" -name SKILL.md 2>/dev/null)" ] || arm_die "no skills are installed"
case $source in
dir\ *)
    diff -r "${source#dir }" "$WT/.claude/skills" >/dev/null || arm_die ".claude/skills differs from ${source#dir }"
    ;;
*)
    ref=$(mktemp -d "${TMPDIR:-/tmp}/arm-skills.XXXXXX")
    MAGUS_HINTS_ENABLED=false "$BENCH_MAGUS_BIN" agent install --dir "$ref" skills --force --skill-form "${source#form }" -q
    current=0
    diff -r "$ref/skills" "$WT/.claude/skills" >/dev/null || current=$?
    rm -rf "$ref"
    [ "$current" = 0 ] || arm_die ".claude/skills is not what $BENCH_MAGUS_BIN renders for ${source#form } form"
    ;;
esac

hook=$(jq -r '.hooks.PreToolUse[] | select(.matcher == "Bash") | .hooks[0].command' "$settings")
event='{"session_id":"arm-probe","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}'
answer=$(cd "$WT" && printf '%s' "$event" | HOME="$WT/.benchmark/home" sh -c "$hook" 2>&1) ||
    arm_die "the wired Bash hook failed: $answer"
case $answer in
*'"hookEventName":"PreToolUse"'*) ;;
*) arm_die "the wired Bash hook gave no PreToolUse answer: $answer" ;;
esac

# MCP is held constant across the arms rather than switched, so its absence is
# an assertion here too and not only in the rampant probe.
arm_check_no_mcp "$WT"

printf 'arm %s: %s carries the agent integration, skills %s\n' "$ARM_NAME" "$WT" "$source"
