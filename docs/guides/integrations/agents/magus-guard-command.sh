#!/usr/bin/env sh
# magus guard hook: judges ONE shell command an agent is about to run.
#
# This file is the source of truth. The docs site embeds it, magus's own
# repository invokes it, and you can download it and do the same. POSIX sh, no
# bashisms; nothing in it is magus-internal.
#
# Contract: reads the host's event as JSON on stdin, selects its command with
# jq, then pipes the command into magus hook. It writes the host's response on
# stdout and exits 0 either way. Override any of the variables below:
#
#   HOST_EVENT_PATH  dot-path to the command inside your host's event
#   HOST_SESSION_PATH  dot-path to the session id inside your host's event
#   HOST_RESPONSE    Go template rendering your host's reply
#   GUARD_AGENT_NAME  the agent host name recorded alongside the observation
#   GUARD_MAGUS_BIN  path to the binary, when it is not on PATH
#   GUARD_UNAVAILABLE_RESPONSE  what to print when magus cannot be found, so a
#                    host can choose its own fail-open or fail-closed stance
#   GUARD_FAILED_RESPONSE  the same, for a magus that IS found but cannot judge
#                    the command (too old for `hook`, cannot load the workspace)
#
# The defaults are Claude Code's event and response shape.
#
# GUARD_AGENT_NAME and the session are ATTRIBUTION, not policy. magus records them on
# its activity event so a reader can tell which host produced an observation;
# neither one can change the verdict, and a host whose event carries no session
# id records none and is judged exactly the same.
#
# GUARD_MAGUS_BIN is deliberately NOT called MAGUS_BIN: the whole MAGUS_* space is
# magus's own configuration surface, so a variable this template invents must stay
# out of it rather than look like a setting magus reads.
#
# On a missing magus this prints a visible notice rather than exiting quietly. A
# bare `command -v magus || exit 0` fails SILENTLY - the guard never runs and
# nothing says so - and an unguarded session you know about beats one you do not.
#
# The line below declares, per guard surface, how much of a verdict this file
# can carry: model (reaches the agent), human (reaches the person only), or none
# (not delivered). It is machine-read by the host-parity gate, which fails the
# build when a decision or surface exists in the guard contract that some host
# was never asked about. Keep it true to what HOST_RESPONSE actually renders.
# magus-guard-template: 4
# magus-guard-coverage: schema=1 host=claude-code,codex surface=command deny=model advise=model pass=none

# Plain assignment, NOT ${VAR:=default}: the response template is full of `}` and
# the first one would terminate a ${...} expansion, silently truncating it.
[ -n "$HOST_EVENT_PATH" ] || HOST_EVENT_PATH='tool_input.command'
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
[ -n "$GUARD_UNAVAILABLE_RESPONSE" ] || GUARD_UNAVAILABLE_RESPONSE='{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"magus guard is NOT running: magus is not on PATH, so its deny and advise rules are unenforced right now. Install magus, or set GUARD_MAGUS_BIN to its path, to restore the guard."}}'
[ -n "$GUARD_FAILED_RESPONSE" ] || GUARD_FAILED_RESPONSE='{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"magus guard is NOT running: the magus binary was found but could not judge this command, so its deny and advise rules are unenforced right now. It is probably too old for the hook subcommand, or cannot load this workspace - run magus hook by hand to see the error, then rebuild or update it to restore the guard."}}'

if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
  printf '%s' "$GUARD_UNAVAILABLE_RESPONSE"
  exit 0
fi

# stdin is a pipe and can only be drained once, so the event is read into a
# variable and selected from twice - the command to judge, and the session id to
# attribute it to. `// empty` keeps a host without that field at the empty
# string rather than the literal "null".
event=$(cat)
session=$(printf '%s' "$event" | jq -r ".$HOST_SESSION_PATH // empty")

# Attribution is BEST EFFORT; the verdict is not.
#
# --agent-name and --session postdate the current magus release, and this template is downloaded and run
# against whatever binary a reader already has. Passing them unconditionally does not degrade the
# guard, it BREAKS it: an older binary rejects the unknown flag, prints its usage to stdout, and
# exits non-zero, so the host receives no verdict at all and every deny and advise rule silently
# stops being enforced. A guard that fails because of a metadata flag has its priorities backwards.
#
# So: try with attribution, and on any failure re-run without it - exactly the call this script made
# before attribution existed. One extra process only on an older binary, and none once the flags are
# in a release.
guard() {
  printf '%s' "$event" | jq -r ".$HOST_EVENT_PATH" | "$GUARD_MAGUS_BIN" hook "$@" -o "template=$HOST_RESPONSE"
}
# A DENY exits non-zero (2) with the verdict on stdout, so a bare `||` retry would treat
# every blocked command as "this binary rejected the attribution flags" and judge it a
# second time - unattributed, and recorded twice in the activity trail. Emptiness alone
# cannot tell the cases apart either, because a pass renders empty on purpose. Both
# together can: a rejected flag prints its usage to STDERR and leaves stdout empty, while
# any real verdict that is not a pass leaves something on stdout.
verdict=$(guard --agent-name "$GUARD_AGENT_NAME" --session "$session" 2>/dev/null)
status=$?
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  verdict=$(guard 2>/dev/null)
  status=$?
fi

# A PASS and a BROKEN GUARD both render nothing, and telling them apart is the whole
# point of this block. A pass exits 0 with empty output because there was nothing to
# say; a binary that cannot run - too old for `hook`, unable to load the workspace,
# half-written by a concurrent build - exits non-zero with empty output, and printing
# that as a pass silently disables every rule with nothing anywhere saying so.
#
# That silence is the failure mode this guard can least afford, because it looks
# exactly like a clean session. The same reasoning is already spelled out above for a
# MISSING binary; a broken one had been left to fail quietly, which is the case that
# actually occurs - a stale binary on PATH outlives a missing one.
#
# Fail OPEN either way. A guard that blocks work because it cannot judge it has its
# priorities backwards, and an unguarded session you know about beats one you do not.
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  printf '%s' "$GUARD_FAILED_RESPONSE"
  exit 0
fi
printf '%s' "$verdict"
