#!/usr/bin/env sh
# magus guard hook for Cursor's beforeShellExecution hook, which runs a PROGRAM
# rather than an inline shell string - hence a wrapper. Copy this directory's
# scripts to .cursor/hooks/ (chmod +x) and point .cursor/hooks.json here.
#
# This file holds ONLY what is Cursor-specific and delegates the rest, so there
# is one implementation of the guard rather than one per host:
#
#   - the command is at the event's TOP LEVEL (`command`), not nested under a
#     tool_input object
#   - Cursor's reply is {"permission": ...}, with agent_message on a denial
#   - Cursor delivers agent_message only on a DENIAL, so `advise` collapses to a
#     plain allow here; those nudges live in the installed skills instead
#   - on a missing magus this ALLOWS. Cursor already fails open on a hook crash
#     or malformed JSON unless the hook sets failClosed, so pretending otherwise
#     would give false assurance. For strict behaviour set failClosed on the hook
#     and change GUARD_UNAVAILABLE_RESPONSE below to a deny.

HOST_EVENT_PATH='command'
HOST_RESPONSE='{{if eq .decision "deny"}}{"permission":"deny","agent_message":{{toJson .reason}}}{{else}}{"permission":"allow"}{{end}}'
GUARD_UNAVAILABLE_RESPONSE='{"permission":"allow"}'
export HOST_EVENT_PATH HOST_RESPONSE GUARD_UNAVAILABLE_RESPONSE

exec "$(dirname "$0")/magus-guard-command.sh"
