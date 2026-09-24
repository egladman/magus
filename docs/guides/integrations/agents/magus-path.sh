#!/usr/bin/env sh
# magus guard hook: judges ONE file path an agent is about to write.
#
# Companion to magus-command.sh, wired to your host's file-editing tool
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
# __MAGUS_AGENT_NAME, HOST_SESSION_PATH and HOST_AGENT_PATH work exactly as they do
# in magus-command.sh: attribution recorded on the activity event; the subagent id
# also selects the job that subagent was spawned for.
#
# Coverage declaration, machine-read by the host-parity gate - see the longer
# note in magus-command.sh. It records what HOST_RESPONSE RENDERS, not
# which rules currently fire, so deny=model is true the moment the arm exists.
#
# No rule asks on this surface today. The arm exists for the same reason the deny arm did
# before its first rule: an installed copy never self-corrects. Claude Code prompts on it;
# Codex does not support a hook ask and no Codex rule prompts for a write, so there it
# renders as a deny.
# magus-guard-template: 17
# magus-guard-coverage: schema=1 host=claude-code surface=path deny=model advise=model pass=none ask=human
# magus-guard-coverage: schema=1 host=codex surface=path deny=model advise=model pass=none ask=model

# Plain assignment, NOT ${VAR:=default}: the response template is full of `}`
# and the first one would terminate a ${...} expansion.
[ -n "$HOST_EVENT_PATH" ] || HOST_EVENT_PATH='tool_input.file_path'
[ -n "$HOST_SESSION_PATH" ] || HOST_SESSION_PATH='session_id'
[ -n "$HOST_AGENT_PATH" ] || HOST_AGENT_PATH='agent_id'
[ -n "$HOST_TRANSCRIPT_PATH" ] || HOST_TRANSCRIPT_PATH='transcript_path'
[ -n "$__MAGUS_AGENT_NAME" ] || __MAGUS_AGENT_NAME='claude-code'
# The one arm on this surface that does not fail open; see the truncated-envelope check
# below. Plain assignment for the same reason as the rest: a `}` would end a ${...}.
[ -n "$__MAGUS_UNREADABLE_RESPONSE" ] || __MAGUS_UNREADABLE_RESPONSE='{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"magus guard could not read this write from the host, so nothing was judged. A payload that arrives truncated reads exactly like an empty one, which is why this is blocked rather than cleared. Retry the call."}}'
# Same split, and the same reason, as in magus-command.sh: a host that
# REJECTS the context key can mark the hook run failed and continue the call, so an
# advisory it cannot take disarms that call rather than merely going unread. No
# host wired to this file is in that position; the flag is there for the one you
# may wire.
if [ -n "$__MAGUS_NO_ADVISE" ]; then
  HOST_ADVISE_BRANCH=''
else
  [ -n "$HOST_ADVISE_BRANCH" ] || HOST_ADVISE_BRANCH='{{else if eq .decision "advise"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .context}}}}'
fi
# HOST_RESPONSE's default is assembled once the event is read, because telling Codex apart
# reads the event; see magus-command.sh.
# Prefer the workspace's own ./magus over PATH. A repository that builds magus, or pins a
# newer one than is installed, keeps its RULES in that binary - and an older PATH copy does
# not fail loudly when it lacks them. It does not recognize the config key that ARMS a rule,
# warns about an unknown field, and returns pass: silent non-enforcement at exit 0. Measured
# 2026-08-13, when a write into a declared notes store was allowed by a binary that predated
# the knowledge.notes key while `magus doctor` reported the guard as fine.
#
# Found by walking UP to the magusfile, not by testing ./magus alone. A hook runs in the
# host's session directory, and that is not always the workspace root: a session opened in
# a subdirectory, or opened in one checkout while the work happens in another, tests a
# ./magus that is not there and falls through to PATH. Where PATH's copy cannot load the
# workspace at all, that is the entire guard failing open - measured 2026-08-27, when a
# piped `magus affected ci` that the rules DO deny ran unjudged. Same upward search for a
# project root that every other ecosystem's runner does.
guard_root=$PWD
while [ -n "$guard_root" ] && [ -z "$__MAGUS_BIN" ]; do
  if [ -f "$guard_root/magusfile.buzz" ]; then
    [ -x "$guard_root/magus" ] && __MAGUS_BIN=$guard_root/magus
    break
  fi
  guard_root=${guard_root%/*}
done
[ -n "$__MAGUS_BIN" ] || __MAGUS_BIN=$(command -v magus 2>/dev/null)

if [ -z "$__MAGUS_BIN" ] || [ ! -x "$__MAGUS_BIN" ]; then
  # Prints nothing by default: for most hosts an empty response means "allow".
  # Set __MAGUS_UNAVAILABLE_RESPONSE for a host that needs an explicit verdict.
  [ -n "$__MAGUS_UNAVAILABLE_RESPONSE" ] && printf '%s' "$__MAGUS_UNAVAILABLE_RESPONSE"
  exit 0
fi

# One drain of stdin, two selections from it: the path to judge, and the session
# id to attribute it to. `// empty` keeps a host without that field at the empty
# string rather than the literal "null".
event=$(cat)

# A payload that opens like an envelope but does not parse is a TRUNCATED one, not a path.
# Judged as text it matches no rule and passes, so it is refused here while its shape still
# says what it was. magus itself denies an unreadable payload for the same reason.
case $event in
  '{'*)
    if ! printf '%s' "$event" | jq -e . >/dev/null 2>&1; then
      printf '%s' "$__MAGUS_UNREADABLE_RESPONSE"
      exit 0
    fi
    ;;
esac

session=$(printf '%s' "$event" | jq -r ".$HOST_SESSION_PATH // empty")
agent=$(printf '%s' "$event" | jq -r ".$HOST_AGENT_PATH // empty")
transcript=$(printf '%s' "$event" | jq -r ".$HOST_TRANSCRIPT_PATH // empty")

# Which payload magus gets: the WHOLE envelope, or the one string HOST_EVENT_PATH selects.
# magus reads a write target off the ENVELOPE under every `*_path` spelling, so selecting one
# dot-path here left NotebookEdit's `notebook_path` unjudged. See magus-path.buzz.
whole_event=1
if [ "$(printf '%s' "$event" | jq -r '(.tool_name // "") | startswith("mcp__")' 2>/dev/null)" != true ] &&
  [ "$(printf '%s' "$event" | jq -r ".$HOST_EVENT_PATH | type" 2>/dev/null)" = string ]; then
  whole_event=
fi

# Codex by name or by its event's turn_id, exactly as magus-command.sh decides it. No
# Codex rule prompts for a write, so there an ask renders as a deny.
if [ -z "$HOST_ASK_BRANCH" ]; then
  if [ "$__MAGUS_AGENT_NAME" = codex ] || [ "$(printf '%s' "$event" | jq -r 'has("turn_id")' 2>/dev/null)" = true ]; then
    HOST_ASK_BRANCH='{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print .reason "\n\nThis write needs the approval of the person you work for, and Codex has no prompt for it. Ask them to make it themselves.")}}}}'
  else
    HOST_ASK_BRANCH='{{else if eq .decision "ask"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":{{toJson .reason}}}}'
  fi
fi
# A decision this file does not know is refused, never allowed; see magus-command.sh.
# Only a reply assembled here claims --renders-ask: a HOST_RESPONSE the reader wrote gets a
# deny from magus rather than an ask it may render as nothing.
renders_ask=
if [ -z "$HOST_RESPONSE" ]; then
  HOST_RESPONSE='{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}'"$HOST_ASK_BRANCH$HOST_ADVISE_BRANCH"'{{else if eq .decision "advise"}}{{else if eq .decision "pass"}}{{else}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson (print "magus guard returned the decision " .decision ", which this hook does not know, so it refuses the call rather than allow it. Update the hook template from the magus docs.")}}}}{{end}}'
  renders_ask=--renders-ask
fi

# Attribution is BEST EFFORT; the verdict is not. --agent-name, --session and --agent postdate the current magus
# release, and an older binary rejects the unknown flag outright - printing usage to stdout and
# exiting non-zero - which leaves the host with no verdict rather than an unattributed one. Try with
# attribution, fall back to the call this script made before it existed.
guard() {
  if [ -n "$whole_event" ]; then
    printf '%s' "$event"
  else
    printf '%s' "$event" | jq -r ".$HOST_EVENT_PATH"
  fi | "$__MAGUS_BIN" shell --path "$@" -o "template=$HOST_RESPONSE"
}
# Same discrimination as magus-command.sh, and for the same reason now that this
# surface can render a deny: a DENY exits non-zero (2) with the verdict on stdout, so a
# bare `||` retry would treat every blocked write as "this binary rejected the attribution
# flags" and judge it a second time - unattributed, and recorded twice in the activity
# trail. Emptiness alone cannot tell the cases apart either, because a pass renders empty
# on purpose. Both together can: a rejected flag prints its usage to STDERR and leaves
# stdout empty, while any real verdict that is not a pass leaves something on stdout.
# shellcheck disable=SC2086
verdict=$(guard --agent-name "$__MAGUS_AGENT_NAME" --transport sh --session "$session" --agent "$agent" --transcript "$transcript" $renders_ask 2>/dev/null)
status=$?
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  verdict=$(guard 2>/dev/null)
  status=$?
fi

# A pass and a broken guard both render nothing; see magus-command.sh for why
# telling them apart matters. Kept identical here so neither surface grows a behavior
# the other lacks - the difference is only that this one has no default message,
# because for most hosts an empty response on this surface already means "allow".
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  [ -n "$__MAGUS_FAILED_RESPONSE" ] && printf '%s' "$__MAGUS_FAILED_RESPONSE"
  exit 0
fi
printf '%s' "$verdict"
