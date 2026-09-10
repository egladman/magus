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
#   REHYDRATE_FORMAT  set it to `json` for a host that reads stdout as a reply
#
# Two channels, because hosts disagree about what a session-start hook's stdout
# IS. Some add plain stdout to the model's context, which is the default here.
# Others parse stdout as a JSON reply and drop anything that is not one, so the
# same text has to arrive as a string field:
#
#   {"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"..."}}
#
# No jq, unlike this file's judging siblings. They need it to SELECT out of an
# event; there is nothing to select here, only text to escape, and a machine
# without jq should still get its checkout handed back.
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
# magus-guard-template: 12

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

# JSON-escapes stdin into one string body: every control character but the line
# break becomes a space (a raw one is not legal inside a JSON string), backslash
# and quote are escaped, and the lines are joined with a literal \n.
#
# The read loop rather than a one-liner because the join has to survive a body
# whose last line carries no newline; that line is what `read` reports as a
# failure with the text still in hand.
rehydrate_escape() {
  tr '\001-\011\013-\037' '[ *]' | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | {
    escaped=''
    while IFS= read -r line; do
      escaped=$escaped$line'\n'
    done
    [ -z "$line" ] || escaped=$escaped$line
    printf '%s' "$escaped"
  }
}

# Captured rather than streamed, because the json arm has to wrap it. stderr is
# discarded: a magus too old for `session --brief` prints its usage there, and
# that would otherwise be injected as this hook's answer.
brief=$("$GUARD_MAGUS_BIN" session --brief 2>/dev/null)

# Nothing from magus is nothing to say, in either channel. The rules line trails
# the brief and points back at it, so on its own it is a sentence about a block
# that was never written, and an envelope carrying only that is worse than none.
[ -n "$brief" ] || exit 0

if [ -n "$guard_root" ] && [ -f "$guard_root/$REHYDRATE_RULES" ]; then
  brief=$(printf '%s\nstanding rules: %s; re-read it, the summary above is not it' \
    "$brief" "$REHYDRATE_RULES")
fi

if [ "$REHYDRATE_FORMAT" = 'json' ]; then
  printf '{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"%s"}}' \
    "$(printf '%s\n' "$brief" | rehydrate_escape)"
  exit 0
fi

printf '%s\n' "$brief"

exit 0
