#!/usr/bin/env sh
# magus guard hook for Cursor's beforeShellExecution hook, which runs a PROGRAM
# rather than an inline shell string - hence a wrapper script. POSIX sh, no
# bashisms. Copy to .cursor/hooks/ (chmod +x) and point .cursor/hooks.json at it.
#
# Two Cursor-specific facts shape this file:
#
#   - The command is at the event's TOP LEVEL (--from-json command), not nested
#     under a tool_input object.
#   - Cursor delivers agent_message to the model only on a DENIAL, not on an
#     allow, so `advise` collapses to a plain allow here. The advisory nudges
#     live in the installed guidance instead.
#
# Stance on a missing magus: this ALLOWS, unlike the OpenCode plugin, which
# blocks. Cursor already fails open on a hook crash or malformed JSON unless the
# hook sets failClosed, so a wrapper pretending otherwise would give false
# assurance. For the strict behaviour, set failClosed on the hook and replace the
# allow below with an explicit deny.

[ -n "$GUARD_MAGUS_BIN" ] || GUARD_MAGUS_BIN=$(command -v magus 2>/dev/null)

if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
  printf '%s' '{"permission":"allow"}'
  exit 0
fi

exec "$GUARD_MAGUS_BIN" agent hook --from-json command \
  -o 'template={{if eq .decision "deny"}}{"permission":"deny","agent_message":{{toJson .reason}}}{{else}}{"permission":"allow"}{{end}}'
