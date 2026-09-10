#!/usr/bin/env sh
# magus session load recipe: turns Claude Code's session store into the magus
# session event contract, one JSON object per line.
#
# This file is the source of truth. The docs site embeds it, magus's own
# repository invokes it, and you can download it and do the same. POSIX sh, no
# bashisms; nothing in it is magus-internal.
#
# Contract, one line per event:
#
#   {"host":"claude-code","session":"<id>","ts":<unix ms>,"cwd":"<abs>",
#    "kind":"shell.command|file.read|file.write|skill.load|hook.output|spawn|magus.call",
#    "ref":"<the host's own id for this event>","text":"<command | path | skill | hook text>",
#    "transcript":"<abs>","outcome":{"exit":null,"denied":false,"interrupted":false}}
#
# Run it with no arguments to pipe the stream into `magus session load`; run it
# with --stdout to read the stream yourself. Override any of:
#
#   HOST_REPO_ROOT      the repository to scope to; default is the git toplevel
#                       of the current directory. A cwd UNDER it counts, which is
#                       what keeps worktrees in
#   HOST_SESSION_STORE  where Claude Code keeps its sessions
#   HOST_PROJECT_DIRS   the directories to walk, space separated. Default is
#                       every project directory under the store whose name
#                       begins with the encoded repo root
#   SESSION_STATE_DIR   where the per-file offsets live
#   SESSION_MAGUS_BIN   path to the binary, when it is not on PATH
#
# The `text` of a spawn is the SUBAGENT TYPE, not the prompt the host records.
# A prompt is the delegating agent's own words about work in progress, it is
# unbounded, and the audit question it would answer ("what was this agent told")
# is not one any report here asks. The type answers the one that IS asked: which
# kind of agent ran, how often, and what it did next.
#
# Re-runs are incremental: each transcript's consumed byte count is checkpointed
# under SESSION_STATE_DIR, and the checkpoint is written only after the whole
# stream is delivered, so a failed load is retried rather than skipped. Byte
# offsets stop at the last COMPLETE line, because the host appends to a file this
# script is reading.
#
# The line below declares, per dimension of the contract, what this host can
# supply: yes when the store carries it, none when it does not. It is machine-read
# by the session-parity gate, which fails the build when a recipe drops a
# dimension or the guide's table disagrees with it. A host that supplies less
# declares less; the report then says unobservable rather than zero.
# magus-guard-template: 11
# magus-session-coverage: schema=1 host=claude-code commands=yes exit=none skills=yes hook-output=yes spawn=yes session-id=yes

# NO `set -e`. Every failure below is a transcript this run does not read, not a
# reason to abandon the ones it can: a single malformed line would otherwise end
# the walk and leave every later session unloaded.

[ -n "$HOST_SESSION_STORE" ] || HOST_SESSION_STORE=$HOME/.claude/projects
[ -n "$SESSION_STATE_DIR" ] || SESSION_STATE_DIR=${XDG_STATE_HOME:-$HOME/.local/state}/magus/session-load/claude-code
[ -n "$HOST_REPO_ROOT" ] || HOST_REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)

session_stdout=
[ "$1" = "--stdout" ] && session_stdout=1

if [ -z "$HOST_REPO_ROOT" ]; then
  echo 'magus session load: no repository here, so there is nothing to scope events to. Run this inside a checkout, or set HOST_REPO_ROOT.' >&2
  exit 0
fi

# jq is the whole extraction. Announce its absence rather than reporting an empty
# session store: a recipe that silently loads nothing looks exactly like a host
# nobody has used, which is the reading this audit exists to make impossible.
if ! command -v jq >/dev/null 2>&1; then
  echo 'magus session load: jq is not installed, so no session events were extracted. Install jq to restore the recipe.' >&2
  exit 0
fi

# Prefer the workspace's own ./magus over PATH, found by walking UP to the
# magusfile - the same resolution the guard templates use, and for the same
# reason: a PATH binary too old for `session load` rejects the subcommand and the
# stream goes nowhere.
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

# Claude Code names a project directory after the cwd it was opened in, with every
# character outside [A-Za-z0-9] replaced by a dash. Encoding the repo root the same
# way and matching on the PREFIX is what picks up worktrees: their paths extend the
# root, so their directory names extend its encoding.
if [ -z "$HOST_PROJECT_DIRS" ]; then
  encoded=$(printf '%s' "$HOST_REPO_ROOT" | LC_ALL=C tr -c 'A-Za-z0-9' '-')
  HOST_PROJECT_DIRS=$(find "$HOST_SESSION_STORE" -maxdepth 1 -type d -name "$encoded*" 2>/dev/null)
fi

work=$(mktemp -d) || exit 0
trap 'rm -rf "$work"' EXIT INT TERM
: > "$work/events"
: > "$work/marks"

# extract reads one transcript's unread tail and writes contract lines.
#
# The fold over `inputs, null` is what lets an outcome reach the event it belongs
# to without holding the file in memory. Claude Code records a tool_use and its
# result as two records, so an event is parked under its id and released when the
# result arrives; the trailing null releases whatever is still parked, which is
# how the last call of an incremental chunk is emitted rather than lost.
extract() {
  jq -c -n --arg host claude-code --arg transcript "$1" --arg root "$2" '
    def ms: try ((sub("\\.[0-9]+";"") | sub("Z?$";"Z") | fromdateiso8601) * 1000) catch 0;
    def kindof($n):
      if $n == "Bash" then "shell.command"
      elif $n == "Read" or $n == "NotebookRead" then "file.read"
      elif $n == "Edit" or $n == "Write" or $n == "MultiEdit" or $n == "NotebookEdit" then "file.write"
      elif $n == "Skill" then "skill.load"
      elif $n == "Agent" or $n == "Task" then "spawn"
      elif ($n | startswith("mcp__magus")) then "magus.call"
      else null end;
    def textof($n; $i):
      if $n == "Bash" then ($i.command // "")
      elif $n == "Skill" then ($i.skill // $i.name // "")
      elif $n == "Agent" or $n == "Task" then ($i.subagent_type // "")
      elif ($n | startswith("mcp__magus")) then $n
      else ($i.file_path // "") end;
    def event($r; $kind; $ref; $text):
      {host: $host, session: ($r.sessionId // ""), ts: ($r.timestamp // "" | ms),
       cwd: ($r.cwd // ""), kind: $kind, ref: $ref, text: $text, transcript: $transcript,
       outcome: {exit: null, denied: false, interrupted: false}};
    foreach (inputs, null) as $r ({p: {}, e: []};
      .e = []
      | if $r == null then .e = [.p[]] | .p = {}
        elif ($r.cwd // "") != $root and (($r.cwd // "") | startswith($root + "/") | not) then .
        elif $r.type == "assistant" then
          reduce ($r.message.content[]? | select(.type == "tool_use")) as $t (.;
            if kindof($t.name) == null then .
            else .p[$t.id] = event($r; kindof($t.name); $t.id; textof($t.name; $t.input)) end)
        elif $r.type == "user" then
          reduce ($r.message.content[]? | select(.type == "tool_result")) as $x (.;
            if (.p | has($x.tool_use_id)) then
              .e += [.p[$x.tool_use_id]
                     | .outcome.denied = (($r.toolDenialKind // null) != null)
                     | .outcome.interrupted = (($r.toolUseResult.interrupted // false) == true)]
              | del(.p[$x.tool_use_id])
            else . end)
        elif $r.type == "attachment" and (($r.attachment.type // "") | startswith("hook_")) then
          .e += [event($r; "hook.output"; ($r.attachment.toolUseID // $r.uuid // "");
                       (($r.attachment.content // $r.attachment.stdout // "") | tostring))]
        else . end;
      .e[])' 2>/dev/null
}

for dir in $HOST_PROJECT_DIRS; do
  [ -d "$dir" ] || continue
  # Subagent transcripts sit a level down, under <sessionId>/subagents/, and carry
  # the orchestrator's session id. Excluded, they take the delegated half of every
  # fanned-out session with them.
  find "$dir" -name '*.jsonl' -type f 2>/dev/null | while read -r transcript; do
    mark=$SESSION_STATE_DIR/$(printf '%s' "$transcript" | cksum | cut -d' ' -f1)
    offset=0
    [ -f "$mark" ] && offset=$(cat "$mark" 2>/dev/null)
    case $offset in *[!0-9]*|'') offset=0;; esac
    size=$(LC_ALL=C wc -c < "$transcript" 2>/dev/null)
    [ -n "$size" ] || continue
    # A file smaller than its checkpoint was rotated or replaced, so the offset
    # describes bytes that no longer exist and reading from it would land mid-record.
    [ "$size" -lt "$offset" ] && offset=0
    [ "$size" -le "$offset" ] && continue

    tail -c "+$((offset + 1))" "$transcript" > "$work/chunk" 2>/dev/null
    # A non-empty last byte means the host is mid-append and the final line is a
    # fragment. Dropping it leaves the checkpoint short, so the next run reads that
    # record whole.
    if [ -n "$(tail -c 1 "$work/chunk" 2>/dev/null)" ]; then
      sed '$d' "$work/chunk" > "$work/whole" 2>/dev/null
    else
      cat "$work/chunk" > "$work/whole"
    fi
    consumed=$(LC_ALL=C wc -c < "$work/whole")
    [ "$consumed" -gt 0 ] || continue

    extract "$transcript" "$HOST_REPO_ROOT" < "$work/whole" >> "$work/events"
    echo "$mark $((offset + consumed))" >> "$work/marks"
  done
done

if [ -n "$session_stdout" ]; then
  cat "$work/events"
else
  "$SESSION_MAGUS_BIN" session load < "$work/events" || exit 1
fi

# Checkpoints are committed only once the stream has been delivered. A load that
# failed leaves every offset where it was, so the retry re-reads the same records
# instead of the audit quietly losing them.
mkdir -p "$SESSION_STATE_DIR" 2>/dev/null || exit 0
while read -r mark position; do
  printf '%s' "$position" > "$mark" 2>/dev/null
done < "$work/marks"
