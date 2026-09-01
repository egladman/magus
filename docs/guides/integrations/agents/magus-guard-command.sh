#!/usr/bin/env sh
# magus guard hook: judges ONE shell command an agent is about to run.
#
# This file is the source of truth. The docs site embeds it, magus's own
# repository invokes it, and you can download it and do the same. POSIX sh, no
# bashisms; nothing in it is magus-internal.
#
# Contract: reads the host's event as JSON on stdin, selects its command with
# jq, then pipes the command into magus session hook. It writes the host's response on
# stdout and exits 0 either way. Override any of the variables below:
#
#   HOST_EVENT_PATH  dot-path to the command inside your host's event
#   HOST_SESSION_PATH  dot-path to the session id inside your host's event
#   HOST_TRANSCRIPT_PATH  dot-path to your host's own log of this session
#   HOST_RESPONSE    Go template rendering your host's reply
#   HOST_ADVISE_BRANCH  the advise arm of that template
#   GUARD_NO_ADVISE  set it when the host has no context-injection channel, so
#                    an advise renders nothing rather than something it rejects
#   GUARD_AGENT_NAME  the agent host name recorded alongside the observation
#   GUARD_MAGUS_BIN  path to the binary, when it is not on PATH
#   GUARD_UNAVAILABLE_RESPONSE  what to print when magus cannot be found, so a
#                    host can choose its own fail-open or fail-closed stance
#   GUARD_FAILED_RESPONSE  the same, for a magus that IS found but cannot judge
#                    the command. Left unset, this file builds one from evidence:
#                    which binary it resolved, that binary's version, and the
#                    error it actually printed
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
# magus-guard-template: 9
# magus-guard-coverage: schema=1 host=claude-code surface=command deny=model advise=model pass=none
# magus-guard-coverage: schema=1 host=codex surface=command deny=model advise=none pass=none

# Plain assignment, NOT ${VAR:=default}: the response template is full of `}` and
# the first one would terminate a ${...} expansion, silently truncating it.
[ -n "$HOST_EVENT_PATH" ] || HOST_EVENT_PATH='tool_input.command'
[ -n "$HOST_SESSION_PATH" ] || HOST_SESSION_PATH='session_id'
[ -n "$HOST_TRANSCRIPT_PATH" ] || HOST_TRANSCRIPT_PATH='transcript_path'
[ -n "$GUARD_AGENT_NAME" ] || GUARD_AGENT_NAME='claude-code'
# The advise arm is split out because not every host has one. Codex's PreToolUse
# REJECTS additionalContext - it treats the key as an error and the hook then fails
# OPEN - so an advisory sent there is not merely dropped, it disarms the guard for
# that call. Codex sets GUARD_NO_ADVISE from codex-hooks.json and declares advise=none.
# A plain `[ -n ... ] ||` cannot express "deliberately empty" - an empty value looks
# unset and gets the default back - and ${VAR-default} is unusable here for the same
# `}` reason as above. So the suppression is its own flag.
if [ -n "$GUARD_NO_ADVISE" ]; then
  HOST_ADVISE_BRANCH=''
else
  [ -n "$HOST_ADVISE_BRANCH" ] || HOST_ADVISE_BRANCH='{{else if eq .decision "advise"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .context}}}}'
fi
[ -n "$HOST_RESPONSE" ] || HOST_RESPONSE='{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}'"$HOST_ADVISE_BRANCH"'{{end}}'
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
while [ -n "$guard_root" ] && [ -z "$GUARD_MAGUS_BIN" ]; do
  if [ -f "$guard_root/magusfile.buzz" ]; then
    [ -x "$guard_root/magus" ] && GUARD_MAGUS_BIN=$guard_root/magus
    break
  fi
  guard_root=${guard_root%/*}
done
[ -n "$GUARD_MAGUS_BIN" ] || GUARD_MAGUS_BIN=$(command -v magus 2>/dev/null)
[ -n "$GUARD_UNAVAILABLE_RESPONSE" ] || GUARD_UNAVAILABLE_RESPONSE='{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"magus guard is NOT running: magus is not on PATH, so its deny and advise rules are unenforced right now. Install magus, or set GUARD_MAGUS_BIN to its path, to restore the guard."}}'

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
transcript=$(printf '%s' "$event" | jq -r ".$HOST_TRANSCRIPT_PATH // empty")

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
  printf '%s' "$event" | jq -r ".$HOST_EVENT_PATH" | "$GUARD_MAGUS_BIN" session hook "$@" -o "template=$HOST_RESPONSE"
}

# guard_failure_notice states WHICH binary went silent, what version it is, and what it
# actually said - the three facts a reader otherwise spends a session collecting.
#
# The text this replaces offered two suspects, "too old for session hook, or cannot load
# this workspace", and the second is not a cause at all: the deny rules need no workspace,
# and a current binary run from an empty directory still denies. Naming a suspect the
# evidence does not support is worse than naming none, because the reader goes and checks it.
#
# It re-runs the guard to capture stderr, which the verdict path discards. One extra
# process, only on the path that is already broken - the same trade the attribution retry
# above makes. WARN lines are dropped because a config the binary is too old to parse warns
# BEFORE it fails, and that warning is a symptom of the same staleness, not the error.
guard_failure_notice() {
  ver=$("$GUARD_MAGUS_BIN" version 2>/dev/null | head -n 1)
  [ -n "$ver" ] || ver='version unreadable'
  why=$(guard 2>&1 >/dev/null | grep -v 'WARN' | head -n 1)
  [ -n "$why" ] || why='it printed no error'
  printf 'magus guard is NOT running: %s (%s) could not judge this command, so its deny and advise rules are unenforced. It said: %s. Rebuild or update THAT binary to restore the guard.' \
    "$GUARD_MAGUS_BIN" "$ver" "$why"
}
# A DENY exits non-zero (2) with the verdict on stdout, so a bare `||` retry would treat
# every blocked command as "this binary rejected the attribution flags" and judge it a
# second time - unattributed, and recorded twice in the activity trail. Emptiness alone
# cannot tell the cases apart either, because a pass renders empty on purpose. Both
# together can: a rejected flag prints its usage to STDERR and leaves stdout empty, while
# any real verdict that is not a pass leaves something on stdout.
verdict=$(guard --agent-name "$GUARD_AGENT_NAME" --session "$session" --transcript "$transcript" 2>/dev/null)
status=$?
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  verdict=$(guard 2>/dev/null)
  status=$?
fi

# A PASS and a BROKEN GUARD both render nothing, and telling them apart is the whole
# point of this block. A pass exits 0 with empty output because there was nothing to
# say; a binary that cannot run - too old for `session hook`, unable to load the workspace,
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
#
# This repeats on every tool call, because nothing here survives between two of them: the
# process is short-lived and the binary that owns magus's own once-per-session state is the
# one that just failed to run. So the notice is kept to a line instead - twenty commits of
# identical text is wallpaper, and wallpaper is how a real failure goes unread.
if [ "$status" -ne 0 ] && [ -z "$verdict" ]; then
  if [ -n "$GUARD_FAILED_RESPONSE" ]; then
    printf '%s' "$GUARD_FAILED_RESPONSE"
  else
    guard_failure_notice | jq -Rc '{hookSpecificOutput:{hookEventName:"PreToolUse",additionalContext:.}}'
  fi
  exit 0
fi
printf '%s' "$verdict"
