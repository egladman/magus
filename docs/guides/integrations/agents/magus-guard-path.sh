#!/usr/bin/env sh
# magus guard hook: judges ONE file path an agent is about to write.
#
# Companion to magus-guard-command.sh, wired to your host's file-editing tool
# rather than its shell tool. POSIX sh, no bashisms.
#
# The declared-output rule here is the one guard rule that is not a heuristic:
# magus reads every target's DECLARED outputs, so a generated file is generated
# by definition and an edit to it would be overwritten by the next run.
#
# That rule ADVISES rather than blocks. magus denies only what cannot be undone;
# a hand-edited generated file is wasteful, not destructive, since regenerating
# erases it. So it explains that the edit will be overwritten and lets the agent
# correct itself, rather than treating it as unable to learn. Every rule on this
# surface says nothing on any uncertainty - no magus, no workspace, an unclaimed
# path - because an advisory fired on a guess trains the reader to ignore it.
#
# HOST_RESPONSE renders BOTH arms even though the rules shipping today only
# advise. That is deliberate and it is why this template exists at version 2.
# These files are COPIED into a reader's config and never self-correct, so a
# deny arm added at the same time as the first denying rule would fail OPEN on
# every already-installed copy: the deny renders empty, magus exits non-zero,
# and the tail below reads empty-output-plus-nonzero as a broken guard and exits
# 0, which every host takes as allow. Shipping the arm first gives installed
# copies a window to update against a rule that is not yet firing.
#
# A host with no file-write hook still gets the command rules; it just misses
# this one. That is a coverage difference to record, not a reason to skip it.
#
# GUARD_AGENT_NAME and HOST_SESSION_PATH work exactly as they do in
# magus-guard-command.sh: attribution recorded on the activity event, never an
# input to the verdict.
#
# Coverage declaration, machine-read by the host-parity gate - see the longer
# note in magus-guard-command.sh. It records what HOST_RESPONSE RENDERS, not
# which rules currently fire, so deny=model is true the moment the arm exists.
# magus-guard-template: 4
# magus-guard-coverage: schema=1 host=claude-code,codex surface=path deny=model advise=model pass=none

# Plain assignment, NOT ${VAR:=default}: the response template is full of `}`
# and the first one would terminate a ${...} expansion.
[ -n "$HOST_EVENT_PATH" ] || HOST_EVENT_PATH='tool_input.file_path'
[ -n "$HOST_SESSION_PATH" ] || HOST_SESSION_PATH='session_id'
[ -n "$GUARD_AGENT_NAME" ] || GUARD_AGENT_NAME='claude-code'
[ -n "$HOST_RESPONSE" ] || HOST_RESPONSE='{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}{{else if eq .decision "advise"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .context}}}}{{end}}'
# Prefer the workspace's own ./magus over PATH. A repository that builds magus, or pins a
# newer one than is installed, keeps its RULES in that binary - and an older PATH copy does
# not fail loudly when it lacks them. It does not recognize the config key that ARMS a rule,
# warns about an unknown field, and returns pass: silent non-enforcement at exit 0. Measured
# 2026-08-13, when a write into a declared notes store was allowed by a binary that predated
# the knowledge.notes key while `magus doctor` reported the guard as fine.
[ -n "$GUARD_MAGUS_BIN" ] || { [ -x ./magus ] && GUARD_MAGUS_BIN=./magus; }
[ -n "$GUARD_MAGUS_BIN" ] || GUARD_MAGUS_BIN=$(command -v magus 2>/dev/null)

if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
  # Prints nothing by default: for most hosts an empty response means "allow".
  # Set GUARD_UNAVAILABLE_RESPONSE for a host that needs an explicit verdict.
  [ -n "$GUARD_UNAVAILABLE_RESPONSE" ] && printf '%s' "$GUARD_UNAVAILABLE_RESPONSE"
  exit 0
fi

# One drain of stdin, two selections from it: the path to judge, and the session
# id to attribute it to. `// empty` keeps a host without that field at the empty
# string rather than the literal "null".
event=$(cat)
session=$(printf '%s' "$event" | jq -r ".$HOST_SESSION_PATH // empty")

# Attribution is BEST EFFORT; the verdict is not. --agent-name and --session postdate the current magus
# release, and an older binary rejects the unknown flag outright - printing usage to stdout and
# exiting non-zero - which leaves the host with no verdict rather than an unattributed one. Try with
# attribution, fall back to the call this script made before it existed.
guard() {
  printf '%s' "$event" | jq -r ".$HOST_EVENT_PATH" | "$GUARD_MAGUS_BIN" hook --path "$@" -o "template=$HOST_RESPONSE"
}
# Same discrimination as magus-guard-command.sh, and for the same reason now that this
# surface can render a deny: a DENY exits non-zero (2) with the verdict on stdout, so a
# bare `||` retry would treat every blocked write as "this binary rejected the attribution
# flags" and judge it a second time - unattributed, and recorded twice in the activity
# trail. Emptiness alone cannot tell the cases apart either, because a pass renders empty
# on purpose. Both together can: a rejected flag prints its usage to STDERR and leaves
# stdout empty, while any real verdict that is not a pass leaves something on stdout.
verdict=$(guard --agent-name "$GUARD_AGENT_NAME" --session "$session" 2>/dev/null)
status=$?
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  verdict=$(guard 2>/dev/null)
  status=$?
fi

# A pass and a broken guard both render nothing; see magus-guard-command.sh for why
# telling them apart matters. Kept identical here so neither surface grows a behavior
# the other lacks - the difference is only that this one has no default message,
# because for most hosts an empty response on this surface already means "allow".
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  [ -n "$GUARD_FAILED_RESPONSE" ] && printf '%s' "$GUARD_FAILED_RESPONSE"
  exit 0
fi
printf '%s' "$verdict"
