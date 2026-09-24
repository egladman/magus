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
# and pipes it into `magus session hook --observe`. It prints NOTHING and always exits
# 0 - see the note on that below, which is load-bearing rather than tidy.
# Override any of the variables below:
#
#   HOST_EVENT_PATH  dot-path to the read path inside your host's event
#   HOST_SESSION_PATH  dot-path to the session id inside your host's event
#   HOST_TRANSCRIPT_PATH  dot-path to your host's own log of this session
#   __MAGUS_BIN  path to the binary, when it is not on PATH
#
# REQUIRED argument: `--agent-name <host>`, the host recorded alongside the observation,
# which the configuration `magus agent harness apply` writes on the command. Without it
# nothing is recorded and a coded message (MGS3024) goes to stderr: an observation filed
# under a guessed host would be wrong, and a silent gap would be invisible.
#
# The defaults are Claude Code's event shape, matching its two siblings. A
# different host overrides the dot-paths.
#
# NO magus-guard-coverage line, and that absence is deliberate rather than an
# oversight: a coverage declaration states how much of a VERDICT a host can
# carry on a guard surface, and this file carries no verdict on no surface. It
# never denies, never advises, and cannot change what your host does next. The
# parity gates ask that question only of artifacts that answer it.
#
# magus-guard-template: 18

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
agent_name=
while [ $# -gt 0 ]; do
  case $1 in
    --agent-name) agent_name=${2-}; [ $# -ge 2 ] && shift; shift ;;
    --agent-name=*) agent_name=${1#--agent-name=}; shift ;;
    *) shift ;;
  esac
done
if [ -z "$agent_name" ]; then
  printf '%s\n' "magus-observe.sh: [MGS3024] this hook was not given --agent-name, so nothing was recorded. Run \`magus agent harness apply\` to rewrite the host's hook configuration; the commands it writes name the host." >&2
  exit 0
fi
# Prefer the workspace's own ./magus over PATH, for the same reason its two siblings do - and
# this file needs it MORE than they do, because it is silent by design. An older PATH copy
# does not know --observe at all: it rejects the flag, prints its usage to a stream this
# script discards, and exits non-zero into an `|| true` - so the observation is simply never
# recorded, forever, with nothing anywhere saying so. Measured 2026-08-14 in magus's own
# repository, where the wiring was correct, the binary was wrong, and the trail held 3252
# events and not one read.
#
# Found by walking UP to the magusfile, not by testing ./magus alone: a hook runs in the
# host's session directory, which is not always the workspace root. The command template
# carries the full reasoning.
guard_root=$PWD
while [ -n "$guard_root" ] && [ -z "$__MAGUS_BIN" ]; do
  if [ -f "$guard_root/magusfile.buzz" ]; then
    [ -x "$guard_root/magus" ] && __MAGUS_BIN=$guard_root/magus
    break
  fi
  guard_root=${guard_root%/*}
done
[ -n "$__MAGUS_BIN" ] || __MAGUS_BIN=$(command -v magus 2>/dev/null)

# An absent observer is SILENT, where an absent guard is loud.
#
# The guard templates announce themselves when magus cannot be found, because an
# unenforced deny rule is a safety fact the reader needs. Nothing is unenforced
# here - there is no rule - so the same announcement would be a per-read
# interruption reporting that an optional record was not written.
if [ -z "$__MAGUS_BIN" ] || [ ! -x "$__MAGUS_BIN" ]; then
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
# missing from `magus session`, the binary is too old. Both streams are
# discarded because a flag-parse error would otherwise reach the host as this
# hook's response on every read.
printf '%s' "$path" | "$__MAGUS_BIN" shell --observe \
  --agent-name "$agent_name" --session "$session" --transcript "$transcript" \
  --event PreToolUse >/dev/null 2>&1

exit 0
