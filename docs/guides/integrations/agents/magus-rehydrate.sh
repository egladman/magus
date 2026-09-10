#!/usr/bin/env sh
# magus rehydrate hook: prints where this checkout stands, for a session that has
# lost its history.
#
# This file is the source of truth. The docs site embeds it, and you can download
# it and wire it yourself. POSIX sh, no bashisms; nothing in it is magus-internal.
#
# Wire it to your host's session-start event, for the compaction and resume cases
# at least. When a host replaces a long session's history with a summary, the model
# keeps working from prose: the branch it is on, what it has already changed, and
# which rules it agreed to all survive only as somebody's retelling, and each
# retelling is a copy of a copy. Whatever this prints lands in that context window
# instead, read off the disk at the moment it prints.
#
# Contract: runs `magus session --brief`, prints what it says, and adds one line
# naming your host's own instruction file. It judges nothing, reads no event, and
# exits 0 whatever happens. Override:
#
#   GUARD_MAGUS_BIN   path to the binary, when it is not on PATH
#   REHYDRATE_RULES   your host's instruction file, relative to the workspace root
#
# The rules line is the one host-shaped part, which is why it is a variable rather
# than something magus prints: magus names the files it ships and can see
# (AGENTS.md, the installed skill directories), and the file YOUR host reads is
# yours to name. It prints only when that file is really there.
#
# NO magus-guard-coverage line, for the same reason magus-checkpoint.sh has none: a
# coverage declaration states how much of a VERDICT a host can carry, and this file
# carries no verdict on no surface. It never denies, never advises, and cannot
# change what your host does next.
#
# magus-guard-template: 11

# NO `set -e`, deliberately, matching every template beside it. A hook that can
# fail is a hook that can break the session it was meant to help.

[ -n "$REHYDRATE_RULES" ] || REHYDRATE_RULES='CLAUDE.md'

# Walk UP to the magusfile: a hook runs in the host's session directory, which is
# not always the workspace root. The walk is unconditional, because the root is
# also what the rules line is resolved against. The command template carries the
# full reasoning for preferring the workspace's own ./magus over PATH.
guard_root=$PWD
while [ -n "$guard_root" ] && [ ! -f "$guard_root/magusfile.buzz" ]; do
  guard_root=${guard_root%/*}
done
if [ -n "$guard_root" ] && [ -z "$GUARD_MAGUS_BIN" ] && [ -x "$guard_root/magus" ]; then
  GUARD_MAGUS_BIN=$guard_root/magus
fi
[ -n "$GUARD_MAGUS_BIN" ] || GUARD_MAGUS_BIN=$(command -v magus 2>/dev/null)

# An absent magus is SILENT, where an absent guard is loud. Nothing here is
# unenforced (there is no rule), so announcing it would open every compacted
# session with a report that an optional context block was not written.
if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
  exit 0
fi

# stderr is discarded: a magus too old for `session --brief` prints its usage
# there, and that would otherwise be injected as this hook's answer.
"$GUARD_MAGUS_BIN" session --brief 2>/dev/null

if [ -n "$guard_root" ] && [ -f "$guard_root/$REHYDRATE_RULES" ]; then
  printf 'standing rules: %s; re-read it, the summary above is not it\n' "$REHYDRATE_RULES"
fi

exit 0
