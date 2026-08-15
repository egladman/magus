#!/usr/bin/env sh
# magus observe hook: records ONE path an agent reached, and judges nothing.
#
# This file is the source of truth. The docs site embeds it, magus's own
# repository invokes it, and you can download it and do the same. POSIX sh, no
# bashisms; nothing in it is magus-internal.
#
# Wire it to the tools that only LOOK - your host's read equivalent. The guard
# templates beside this one handle the tools that ACT. Do not point a read tool
# at those: a read event carries a file path, so the write rules would advise
# "you are editing a declared output" at a file the agent merely opened.
# --observe is what separates the two, and only this wrapper can set it,
# because only it knows which of your host's tools look.
#
# Contract: reads the host's event as JSON on stdin, selects the path with jq,
# and pipes it into `magus hook --observe`. It prints NOTHING and always exits
# 0 - see the note on that below, which is load-bearing rather than tidy.
# Override any of the variables below:
#
#   HOST_EVENT_PATH  dot-path to the read path inside your host's event
#   HOST_SESSION_PATH  dot-path to the session id inside your host's event
#   HOST_TRANSCRIPT_PATH  dot-path to your host's own log of this session
#   GUARD_AGENT_NAME  the agent host name recorded alongside the observation
#   GUARD_MAGUS_BIN  path to the binary, when it is not on PATH
#
# The defaults are Claude Code's event shape, matching its two siblings. A
# different host overrides the dot-paths and passes its own GUARD_AGENT_NAME,
# exactly as codex-hooks.json already does for the guard templates.
#
# NO magus-guard-coverage line, and that absence is deliberate rather than an
# oversight: a coverage declaration states how much of a VERDICT a host can
# carry on a guard surface, and this file carries no verdict on no surface. It
# never denies, never advises, and cannot change what your host does next. The
# parity gates ask that question only of artifacts that answer it.
#
# magus-guard-template: 6

# NO `set -e`, deliberately, and neither sibling uses it either.
#
# Under `set -e` a jq that is missing (127) or handed a payload it cannot parse
# aborts this script mid-way with jq's own message on stderr - on EVERY read.
# Worse, a PreToolUse hook's exit status is not advisory on every host: some
# treat a specific non-zero code as "block this tool call", so a malformed event
# could stop the agent from reading anything at all. An optional record must
# never be able to do that, so every failure below is swallowed and the script
# ends at `exit 0` on all paths.

[ -n "$HOST_EVENT_PATH" ] || HOST_EVENT_PATH='tool_input.file_path'
[ -n "$HOST_SESSION_PATH" ] || HOST_SESSION_PATH='session_id'
[ -n "$HOST_TRANSCRIPT_PATH" ] || HOST_TRANSCRIPT_PATH='transcript_path'
[ -n "$GUARD_AGENT_NAME" ] || GUARD_AGENT_NAME='claude-code'
# Prefer the workspace's own ./magus over PATH, for the same reason its two siblings do - and
# this file needs it MORE than they do, because it is silent by design. An older PATH copy
# does not know --observe at all: it rejects the flag, prints its usage to a stream this
# script discards, and exits non-zero into an `|| true` - so the observation is simply never
# recorded, forever, with nothing anywhere saying so. Measured 2026-08-14 in magus's own
# repository, where the wiring was correct, the binary was wrong, and the trail held 3252
# events and not one read.
[ -n "$GUARD_MAGUS_BIN" ] || { [ -x ./magus ] && GUARD_MAGUS_BIN=./magus; }
[ -n "$GUARD_MAGUS_BIN" ] || GUARD_MAGUS_BIN=$(command -v magus 2>/dev/null)

# An absent observer is SILENT, where an absent guard is loud.
#
# The guard templates announce themselves when magus cannot be found, because an
# unenforced deny rule is a safety fact the reader needs. Nothing is unenforced
# here - there is no rule - so the same announcement would be a per-read
# interruption reporting that an optional record was not written.
if [ -z "$GUARD_MAGUS_BIN" ] || [ ! -x "$GUARD_MAGUS_BIN" ]; then
  exit 0
fi

# stdin is a pipe and can only be drained once, so the event is read into a
# variable and selected from more than once. `// empty` keeps a host without one
# of these fields at the empty string rather than the literal "null", and jq's
# stderr is discarded because a payload this script cannot parse is a record it
# will not write, not news for the person trying to read a file.
#
# The path is extracted rather than piping the whole event through, because a
# payload magus does not recognize as an envelope is judged as the literal text
# it is - and for a search that carries a pattern but no path, that would record
# the entire event, query text included, as the thing the agent reached.
event=$(cat)
path=$(printf '%s' "$event" | jq -r ".$HOST_EVENT_PATH // empty" 2>/dev/null)

# Nothing to record is not a failure: a host event that names no path has no
# reach to report, and inventing one would claim a file the host never named.
# This gate comes BEFORE the remaining selections so the common no-op case - any
# tool whose event carries no read path - costs one jq rather than three.
[ -n "$path" ] || exit 0

session=$(printf '%s' "$event" | jq -r ".$HOST_SESSION_PATH // empty" 2>/dev/null)
transcript=$(printf '%s' "$event" | jq -r ".$HOST_TRANSCRIPT_PATH // empty" 2>/dev/null)

# A magus too old for --observe rejects the flag and exits non-zero. That is a
# real state worth knowing about once, but not once per read, so it is reported
# through the trail's own absence rather than through the session: if reads are
# missing from `magus activity`, the binary is too old. Both streams are
# discarded because a flag-parse error would otherwise reach the host as this
# hook's response on every read.
printf '%s' "$path" | "$GUARD_MAGUS_BIN" hook --observe \
  --agent-name "$GUARD_AGENT_NAME" --session "$session" --transcript "$transcript" \
  --event PreToolUse >/dev/null 2>&1

exit 0
