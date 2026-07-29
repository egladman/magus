#!/usr/bin/env sh
# magus guard hook: judges ONE shell command an agent is about to run.
#
# This file is the source of truth. The docs site embeds it, magus's own
# repository invokes it, and you can download it and do the same. POSIX sh, no
# bashisms; nothing in it is magus-internal.
#
# Contract: reads the host's event as JSON on stdin, writes the host's response
# on stdout, exits 0 either way. Override any of the three variables below:
#
#   HOST_EVENT_PATH  dot-path to the command inside your host's event
#   HOST_RESPONSE    Go template rendering your host's reply
#   GUARD_MAGUS_BIN  path to the binary, when it is not on PATH
#   GUARD_UNAVAILABLE_RESPONSE  what to print when magus cannot be found, so a
#                    host can choose its own fail-open or fail-closed stance
#
# The defaults are Claude Code's event and response shape.
#
# GUARD_MAGUS_BIN is deliberately NOT called MAGUS_BIN: the whole MAGUS_* space is
# magus's own configuration surface, so a variable this template invents must stay
# out of it rather than look like a setting magus reads.
#
# On a missing magus this prints a visible notice rather than exiting quietly. A
# bare `command -v magus || exit 0` fails SILENTLY - the guard never runs and
# nothing says so - and an unguarded session you know about beats one you do not.

# Plain assignment, NOT ${VAR:=default}: the response template is full of `}` and
# the first one would terminate a ${...} expansion, silently truncating it.
[ -n "$HOST_EVENT_PATH" ] || HOST_EVENT_PATH='tool_input.command'
[ -n "$HOST_RESPONSE" ] || HOST_RESPONSE='{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}{{else if eq .decision "advise"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":{{toJson .context}}}}{{end}}'
[ -n "$GUARD_MAGUS_BIN" ] || GUARD_MAGUS_BIN=$(command -v magus 2>/dev/null)
[ -n "$GUARD_UNAVAILABLE_RESPONSE" ] || GUARD_UNAVAILABLE_RESPONSE='{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"magus guard is NOT running: magus is not on PATH, so its deny and advise rules are unenforced right now. Install magus, or set GUARD_MAGUS_BIN to its path, to restore the guard."}}'

if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
  printf '%s' "$GUARD_UNAVAILABLE_RESPONSE"
  exit 0
fi

exec "$GUARD_MAGUS_BIN" agent hook --from-json "$HOST_EVENT_PATH" -o "template=$HOST_RESPONSE"
