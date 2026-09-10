#!/usr/bin/env sh
# magus session load recipe: turns `opencode export` output into the magus
# session event contract, one JSON object per line.
#
# This file is the source of truth. The docs site embeds it, and you can download
# it and run it yourself. POSIX sh, no bashisms; nothing in it is magus-internal.
# Its Claude Code sibling carries the full contract; the fields are identical.
#
# Run it with no arguments to pipe the stream into `magus session load`; run it
# with --stdout to read the stream yourself. Override any of:
#
#   HOST_REPO_ROOT      the repository to scope to; default is the git toplevel
#                       of the current directory. A cwd UNDER it counts, which is
#                       what keeps worktrees in
#   HOST_SESSION_IDS    the sessions to export, space separated. Default is every
#                       id `opencode sessions` lists
#   HOST_OPENCODE_BIN   path to the opencode binary, when it is not on PATH
#   SESSION_STATE_DIR   where the per-session part counts live
#   SESSION_MAGUS_BIN   path to the magus binary, when it is not on PATH
#
# This one reads a CONTRACT rather than a store: `opencode export` is documented
# output, where the other two hosts' files are de-facto shapes versioned per
# record. So it shells out per session instead of walking a directory, and there
# is no partial-line handling to do - each export is one complete document.
#
# OpenCode is the only host of the three that records a command's exit code, and
# the only one with neither hook records nor a spawn part. The coverage line says
# both; a report reading it says unobservable, never zero.
# magus-guard-template: 11
# magus-session-coverage: schema=1 host=opencode commands=yes exit=yes skills=yes hook-output=none spawn=none session-id=yes

# NO `set -e`: a session whose export fails is not a reason to abandon the rest.

[ -n "$SESSION_STATE_DIR" ] || SESSION_STATE_DIR=${XDG_STATE_HOME:-$HOME/.local/state}/magus/session-load/opencode
[ -n "$HOST_REPO_ROOT" ] || HOST_REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
[ -n "$HOST_OPENCODE_BIN" ] || HOST_OPENCODE_BIN=$(command -v opencode 2>/dev/null)

session_stdout=
[ "$1" = "--stdout" ] && session_stdout=1

if [ -z "$HOST_REPO_ROOT" ]; then
  echo 'magus session load: no repository here, so there is nothing to scope events to. Run this inside a checkout, or set HOST_REPO_ROOT.' >&2
  exit 0
fi

if ! command -v jq >/dev/null 2>&1; then
  echo 'magus session load: jq is not installed, so no session events were extracted. Install jq to restore the recipe.' >&2
  exit 0
fi

if [ -z "$HOST_OPENCODE_BIN" ] || [ ! -x "$HOST_OPENCODE_BIN" ]; then
  echo 'magus session load: opencode is not on PATH, so no session events were extracted. Set HOST_OPENCODE_BIN to its path.' >&2
  exit 0
fi

if [ -z "$SESSION_MAGUS_BIN" ]; then
  session_root=$PWD
  while [ -n "$session_root" ]; do
    if [ -f "$session_root/magusfile.buzz" ]; then
      [ -x "$session_root/magus" ] && SESSION_MAGUS_BIN=$session_root/magus
      break
    fi
    session_root=${session_root%/*}
  done
fi
[ -n "$SESSION_MAGUS_BIN" ] || SESSION_MAGUS_BIN=$(command -v magus 2>/dev/null)

if [ -z "$session_stdout" ] && { [ -z "$SESSION_MAGUS_BIN" ] || [ ! -x "$SESSION_MAGUS_BIN" ]; }; then
  echo 'magus session load: magus is not on PATH, so the extracted events were dropped rather than loaded. Set SESSION_MAGUS_BIN, or pass --stdout to read the stream yourself.' >&2
  exit 0
fi

[ -n "$HOST_SESSION_IDS" ] || HOST_SESSION_IDS=$("$HOST_OPENCODE_BIN" sessions --json 2>/dev/null | jq -r '.[].id // empty' 2>/dev/null)

work=$(mktemp -d) || exit 0
trap 'rm -rf "$work"' EXIT INT TERM
: > "$work/events"
: > "$work/marks"

# An export is one document, so the checkpoint counts PARTS rather than bytes:
# the first N tool parts of a session are the ones already loaded.
extract() {
  jq -c --arg host opencode --arg transcript "$1" --arg root "$2" --argjson skip "$3" '
    (.session.directory // .directory // "") as $cwd
    | (.session.id // .id // "") as $sid
    | if $cwd != $root and ($cwd | startswith($root + "/") | not) then empty else
        [.parts[]? // (.messages[]?.parts[]? // empty) | select(.callID != null)]
        | .[$skip:][]
        | . as $p
        | (if $p.tool == "bash" then "shell.command"
           elif $p.tool == "skill" then "skill.load"
           elif $p.tool == "read" then "file.read"
           elif $p.tool == "write" or $p.tool == "edit" or $p.tool == "patch" then "file.write"
           else null end) as $kind
        | select($kind != null)
        | {host: $host, session: $sid,
           ts: (($p.state.time.start // $p.time.start // 0) | floor),
           cwd: $cwd, kind: $kind, ref: $p.callID,
           text: (if $p.tool == "bash" then ($p.state.input.command // "")
                  elif $p.tool == "skill" then ($p.state.input.name // "")
                  else ($p.state.input.filePath // $p.state.input.path // "") end),
           transcript: $transcript,
           outcome: {exit: ($p.state.metadata.exit // null),
                     denied: (($p.state.status // "") == "error"),
                     interrupted: false}}
      end' 2>/dev/null
}

for sid in $HOST_SESSION_IDS; do
  mark=$SESSION_STATE_DIR/$(printf '%s' "$sid" | cksum | cut -d' ' -f1)
  skip=0
  [ -f "$mark" ] && skip=$(cat "$mark" 2>/dev/null)
  case $skip in *[!0-9]*|'') skip=0;; esac

  "$HOST_OPENCODE_BIN" export "$sid" > "$work/export" 2>/dev/null || continue
  parts=$(jq '[.parts[]? // (.messages[]?.parts[]? // empty) | select(.callID != null)] | length' "$work/export" 2>/dev/null)
  case $parts in *[!0-9]*|'') continue;; esac
  [ "$parts" -le "$skip" ] && continue

  extract "opencode://$sid" "$HOST_REPO_ROOT" "$skip" < "$work/export" >> "$work/events"
  echo "$mark $parts" >> "$work/marks"
done

if [ -n "$session_stdout" ]; then
  cat "$work/events"
  # Nothing was delivered to magus, so the checkpoints stay where they were.
  exit 0
fi
"$SESSION_MAGUS_BIN" session load < "$work/events" || exit 1

mkdir -p "$SESSION_STATE_DIR" 2>/dev/null || exit 0
while read -r mark position; do
  printf '%s' "$position" > "$mark" 2>/dev/null
done < "$work/marks"
