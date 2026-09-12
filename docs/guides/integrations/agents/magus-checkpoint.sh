#!/usr/bin/env sh
# magus checkpoint hook: records where the work stands when a session stops.
#
# This file is the source of truth. The docs site embeds it, magus's own
# repository invokes it, and you can download it and do the same. POSIX sh, no
# bashisms; nothing in it is magus-internal.
#
# Wire it to your host's stop or session-end event. It records the revision,
# branch and dirtiness of the tree, plus your host's session id and transcript
# path as opaque pointers, so that whoever comes back to this repository - you
# tomorrow, or another session - reads `magus session` instead of reconstructing
# where the work stopped. That reconstruction is the cost this exists to remove:
# it was measured at a session id passed by hand, a guessed transcript location,
# and three failed commands before it emerged the work had never been pushed.
#
# Contract: pipes your host's event, unread, into `magus session checkpoint`.
# magus takes the two pointers only a host knows out of the envelope and ignores
# the rest; nothing in the payload becomes the note, because a note is a sentence
# a person writes. It prints NOTHING and always exits 0. Override:
#
#   GUARD_AGENT_NAME  the agent host name recorded alongside the checkpoint
#   GUARD_MAGUS_BIN   path to the binary, when it is not on PATH
#
# A host whose envelope spells those fields differently passes them as flags
# instead - `--session` and `--transcript` outrank the envelope - and a host that
# cannot supply either still records a usable checkpoint, because the part that
# matters is read from the tree rather than from the event.
#
# There is no jq here, unlike its siblings. This wrapper selects nothing: magus
# parses the envelope itself, so a machine without jq records a checkpoint rather
# than silently recording none.
#
# NO magus-guard-coverage line, for the same reason magus-guard-observe.sh has
# none: a coverage declaration states how much of a VERDICT a host can carry, and
# this file carries no verdict on no surface. It never denies, never advises, and
# cannot change what your host does next.
#
# magus-guard-template: 13

# NO `set -e`, deliberately, matching every template beside it. A hook that can
# fail is a hook that can break the session it was meant to observe, and a record
# of where the work stopped is worth strictly less than the work.

[ -n "$GUARD_AGENT_NAME" ] || GUARD_AGENT_NAME='claude-code'
# Prefer the workspace's own ./magus over PATH, found by walking UP to the
# magusfile: a hook runs in the host's session directory, which is not always the
# workspace root. The command template carries the full reasoning.
guard_root=$PWD
while [ -n "$guard_root" ] && [ -z "$GUARD_MAGUS_BIN" ]; do
  if [ -f "$guard_root/magusfile.buzz" ]; then
    [ -x "$guard_root/magus" ] && GUARD_MAGUS_BIN=$guard_root/magus
    break
  fi
  guard_root=${guard_root%/*}
done
[ -n "$GUARD_MAGUS_BIN" ] || GUARD_MAGUS_BIN=$(command -v magus 2>/dev/null)

# An absent recorder is SILENT, where an absent guard is loud. Nothing here is
# unenforced - there is no rule - so announcing it would interrupt the end of
# every session to report that an optional record was not written.
if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
  exit 0
fi

# Both streams are discarded: a magus too old for `session checkpoint` prints its
# usage, and that would otherwise reach the host as this hook's response every
# time a session ends. The absence shows up where it is actionable instead - as
# an empty checkpoint list in `magus session`.
"$GUARD_MAGUS_BIN" session checkpoint --agent-name "$GUARD_AGENT_NAME" >/dev/null 2>&1

exit 0
