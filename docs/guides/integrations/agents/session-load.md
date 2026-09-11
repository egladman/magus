---
title: Session load recipes
description: Per-host recipes that turn an agent host's own session log into the magus session event contract, so an audit can ask which skills loaded, whether the guard fired, and what ran unjudged.
tags: [agents, session, audit, guard, claude code, codex, opencode]
---

# Session load recipes

The guard is magus's record of itself, and there are questions it cannot answer
by construction: whether a denied command was actually abandoned, which skills an
agent loaded, and what ran in a session where the hook was never wired. A host's
own session log is the independent witness for all three.

magus does not read that log. Extraction is yours, one recipe per host, exactly
as the [guard hook templates](guard-templates.md) are yours: files you download,
edit, and own from then on.
[Doctrine](../../../doctrine.md#the-host-wiring-is-yours) records that trade and
what it costs you. The reason is narrower than it sounds. Only OpenCode publishes
an export contract; the other two formats are de-facto and versioned per record,
so a parser inside magus would make every host format change a magus release.

## What loading is for

A loaded session answers questions the trail alone cannot:

- which skills loaded, how often, and after which advisory;
- whether magus's guard spoke at all in a session, and what it said;
- what a command would be judged as TODAY, re-judged offline against the current
  rules rather than against the rules that were installed when it ran;
- which commands ran with no guard record at all, found by joining the host's log
  against magus's own trail for the same session id.

That last one is the join. Neither store answers it alone. `magus session show
<id>` makes it: below the loaded transcript it reports what the guard trail in
the current checkout observed for that host session id, how many of those calls
it denied, the lease they ran under, and the sub-agents the session spawned.
The same join reaches review: `magus diff --impact` names the sessions that
wrote each changed file from both stores, so a session no hook was wired for
still appears once its transcript is loaded.

## The contract

A recipe emits one JSON object per line on stdout and pipes it into
`magus session load`:

```json
{
  "host": "claude-code",
  "session": "<id>",
  "ts": 1788955202000,
  "cwd": "/abs/path",
  "kind": "shell.command",
  "ref": "<the host's own id for this event>",
  "text": "<command | path | skill name | hook text>",
  "transcript": "/abs/path",
  "outcome": { "exit": null, "denied": false, "interrupted": false }
}
```

`kind` is one of `shell.command`, `file.read`, `file.write`, `skill.load`,
`hook.output`, `spawn`, `magus.call`. `magus.call` is a direct tool call to
magus, which never appears as a shell command and would otherwise be invisible.

Four things are worth knowing before writing your own:

- `ref` is the host's id for the thing the event is ABOUT, not for the record.
  A `hook.output` therefore shares its ref with the `shell.command` the guard
  spoke about, because that join is the point. Deduplication keys on
  `(host, session, kind, ref)`.
- `ts` is unix milliseconds. `exit` is `null`, not `0`, where the host records no
  exit status; a zero there would be a measurement nobody made.
- `transcript` points at the original so a reader can open it. Where a host has
  no file, the pointer is the command that reproduces the export
  (`opencode://<id>`).
- `text` for a spawn is the subagent TYPE, not the prompt. A prompt is unbounded,
  it is the delegating agent's own words about work in progress, and no report
  here asks what an agent was told.

## Session load across hosts

Every recipe emits the same contract. What differs is what its host's log
records, and a host that supplies less declares less.

| host        | commands | exit | skills | hook output | spawn | session id |
| ----------- | -------- | ---- | ------ | ----------- | ----- | ---------- |
| Claude Code | yes      | none | yes    | yes         | yes   | yes        |
| Codex       | yes      | none | none   | none        | yes   | yes        |
| OpenCode    | yes      | yes  | yes    | none        | none  | yes        |

A report reads this table and says **unobservable** for a `none`, never zero.
Zero is a measurement; unobservable is the absence of one, and collapsing the two
turns a host with a thinner log into a host whose agents look better behaved.

The table is not prose. Each recipe carries the same statement in a line the
build reads:

```sh
grep magus-session-coverage magus-session-load-claude-code.sh
```

```text
# magus-session-coverage: schema=1 host=claude-code commands=yes exit=none skills=yes hook-output=yes spawn=yes session-id=yes
```

A recipe that drops a dimension fails the build, and so does a table cell that
disagrees with one. Silence is the bug: an undeclared dimension is one nobody was
asked about.

## One caveat that is not a coverage gap

Claude Code writes a hook record only when the hook produced OUTPUT. A guard that
passed silently and a guard that was never wired leave the same nothing behind.
Measured over 98,233 Bash calls in one 21-day window: 14,257 carried a hook
record, and not one of the 12,012 successes carried an empty one. So a recipe
emits what is there and never infers absence, and "this command was unguarded" is
a verdict for the join against magus's trail, not for the extraction.

## Running a recipe

```sh
sh magus-session-load-claude-code.sh            # extract and load
sh magus-session-load-claude-code.sh --stdout   # read the stream yourself
```

Each one scopes to a repository (`HOST_REPO_ROOT`, defaulting to the git toplevel
of the current directory) and keeps a per-file checkpoint under
`${XDG_STATE_HOME:-~/.local/state}/magus/session-load/<host>/`, so a re-run reads
only what is new. The checkpoint moves only after the whole stream is delivered:
a failed load is retried, never skipped. `--stdout` delivers it to you, so it
moves the checkpoint too; to look without consuming, point `SESSION_STATE_DIR` at
a scratch directory for that run.

Each file's header lists the variables it takes. Every one of them announces
itself on stderr when it cannot run, rather than exiting quietly, because a
recipe that extracted nothing looks exactly like a host nobody used.

## Checking whether your copy is current

The recipes carry the same version marker the guard templates do, for the same
reason: once you copy one it is yours, magus cannot reach it again, and nothing
about your copy says how old it is.

```sh
grep magus-guard-template magus-session-load-claude-code.sh
```

## Claude Code

Sessions are JSONL under `~/.claude/projects/<encoded-cwd>/`, with subagent
transcripts a level down under `<sessionId>/subagents/`. Both are read: excluded,
the delegated half of every fanned-out session goes with them.

```sh
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
```

## Codex

Rollouts are JSONL under `~/.codex/sessions/YYYY/MM/DD/`. Nothing names them
after a repository, so each is opened and scoped from the `session_meta` record
inside it. Codex records no skill loads and no hook output, and its only
exit-like signal describes a patch rather than a command.

```sh
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
# magus-guard-template: 11
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
```

## OpenCode

`opencode export <sessionID>` is documented output, so this recipe reads a
contract rather than a store. OpenCode is the only one of the three that records
a command's exit code, and the only one with no hook records and no spawn part.

```sh
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
else
  "$SESSION_MAGUS_BIN" session load < "$work/events" || exit 1
fi

mkdir -p "$SESSION_STATE_DIR" 2>/dev/null || exit 0
while read -r mark position; do
  printf '%s' "$position" > "$mark" 2>/dev/null
done < "$work/marks"
```
