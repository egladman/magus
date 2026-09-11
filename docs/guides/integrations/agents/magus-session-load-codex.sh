#!/usr/bin/env sh
# magus session load recipe: turns Codex's rollout files into the magus session
# event contract, one JSON object per line.
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
#   HOST_SESSION_STORE  where Codex keeps its rollout files
#   SESSION_STATE_DIR   where the per-file offsets live
#   SESSION_MAGUS_BIN   path to the binary, when it is not on PATH
#
# Codex records no skill loads and no hook output, and its only exit-like signal
# is patch_apply_end's success flag, which describes a patch rather than a
# command. The coverage line says so, and a report reading it says unobservable
# for those dimensions rather than zero. Declaring commands=yes on the strength
# of what the other hosts supply is the failure this line exists to prevent.
# magus-guard-template: 13
# magus-session-coverage: schema=1 host=codex commands=yes exit=none skills=none hook-output=none spawn=yes session-id=yes

# NO `set -e`: a rollout this run cannot read is not a reason to abandon the rest.

[ -n "$HOST_SESSION_STORE" ] || HOST_SESSION_STORE=$HOME/.codex/sessions
[ -n "$SESSION_STATE_DIR" ] || SESSION_STATE_DIR=${XDG_STATE_HOME:-$HOME/.local/state}/magus/session-load/codex
[ -n "$HOST_REPO_ROOT" ] || HOST_REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)

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

work=$(mktemp -d) || exit 0
trap 'rm -rf "$work"' EXIT INT TERM
: > "$work/events"
: > "$work/marks"

# Codex nests rollouts by date and names none of them after the repository, so
# every file is opened and scoped from the session_meta record inside it. The
# session id and cwd arrive on that first record and are carried forward, which
# is why this folds state rather than mapping each line independently.
extract() {
  jq -c -n --arg host codex --arg transcript "$1" --arg root "$2" '
    def ms: try ((sub("\\.[0-9]+";"") | sub("Z?$";"Z") | fromdateiso8601) * 1000) catch 0;
    def event($r; $kind; $ref; $text):
      {host: $host, session: .sid, ts: ($r.timestamp // "" | ms), cwd: .cwd,
       kind: $kind, ref: $ref, text: $text, transcript: $transcript,
       outcome: {exit: null, denied: false, interrupted: false}};
    foreach inputs as $r ({sid: "", cwd: "", e: []};
      .e = []
      | if $r.type == "session_meta" then
          .sid = ($r.payload.session_id // "") | .cwd = ($r.payload.cwd // "")
        elif .cwd != $root and (.cwd | startswith($root + "/") | not) then .
        elif $r.type == "custom_tool_call" and ($r.payload.name // "") == "exec" then
          .e += [event($r; "shell.command"; ($r.payload.call_id // "");
                       (($r.payload.input | if type == "object" then (.command // "") else . end) | tostring))]
        elif $r.type == "spawn_agent" then
          .e += [event($r; "spawn"; ($r.payload.call_id // $r.payload.agent_id // "");
                       ($r.payload.name // ""))]
        else . end;
      .e[])' 2>/dev/null
}

find "$HOST_SESSION_STORE" -name 'rollout-*.jsonl' -type f 2>/dev/null | while read -r transcript; do
  mark=$SESSION_STATE_DIR/$(printf '%s' "$transcript" | cksum | cut -d' ' -f1)
  offset=0
  [ -f "$mark" ] && offset=$(cat "$mark" 2>/dev/null)
  case $offset in *[!0-9]*|'') offset=0;; esac
  size=$(LC_ALL=C wc -c < "$transcript" 2>/dev/null)
  [ -n "$size" ] || continue
  [ "$size" -lt "$offset" ] && offset=0
  [ "$size" -le "$offset" ] && continue

  # The whole file, never the unread tail: session_meta is the FIRST record and
  # carries the session id and cwd every later record is scoped by, so a chunk
  # starting after it has nothing to scope. Re-reading is cheap here because a
  # rollout closes when its session ends, and only the open one grows.
  cat "$transcript" > "$work/chunk" 2>/dev/null
  if [ -n "$(tail -c 1 "$work/chunk" 2>/dev/null)" ]; then
    sed '$d' "$work/chunk" > "$work/whole" 2>/dev/null
  else
    cat "$work/chunk" > "$work/whole"
  fi
  consumed=$(LC_ALL=C wc -c < "$work/whole")
  [ "$consumed" -gt "$offset" ] || continue

  # Emitting from byte 0 every time and letting `session load` dedup on
  # (host, session, kind, ref) is the trade this host's format forces. The
  # checkpoint still earns its place: a rollout whose size has not moved is
  # skipped entirely, which is every closed session after the first run.
  extract "$transcript" "$HOST_REPO_ROOT" < "$work/whole" >> "$work/events"
  echo "$mark $consumed" >> "$work/marks"
done

if [ -n "$session_stdout" ]; then
  cat "$work/events"
else
  "$SESSION_MAGUS_BIN" session load < "$work/events" || exit 1
fi

mkdir -p "$SESSION_STATE_DIR" 2>/dev/null || exit 0
while read -r mark position; do
  printf '%s' "$position" > "$mark" 2>/dev/null
done < "$work/marks"
