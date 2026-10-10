---
title: Session load adapters
description: Per-host adapters that turn an agent host's own session log into the magus session event contract, so an audit can ask which skills loaded, whether the guard fired, and what ran unjudged.
tags: [agents, session, audit, guard, claude code, codex, opencode]
---

# Session load adapters

The guard is magus's record of itself, and there are questions it cannot answer
by construction: whether a denied command was abandoned, which skills an
agent loaded, and what ran in a session where the hook was never wired. A host's
own session log is the independent witness for all three.

magus does not read that log. Extraction is yours, one adapter per host, exactly
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
  against magus's own trail for the same session id;
- which of magus's own suggestions were taken, per hint id. See
  [Hint uptake](#hint-uptake).

That last one is the join. Neither store answers it alone. `magus session show
<id>` makes it: below the loaded transcript it reports what the guard trail in
the current checkout observed for that host session id, how many of those calls
it denied, the lease they ran under, and the subagents the session spawned.
The same join reaches review: `magus diff --impact` names the sessions that
wrote each changed file from both stores, so a session no hook was wired for
still appears once its transcript is loaded.

## Hint uptake

Every `next` entry magus prints on a result carries a stable id, and a call that
serves one records it beside the guard's advisory markers, in this checkout's
cache directory:

```text
.magus/advisories/<session>.served-next
{"ts":1789124711213,"id":"query-explain","argv":["./magus","explain","spell:go"]}
```

Only what was served is recorded, and what is served depends on who is
asking. The entries are filtered for the acting lease's role first: a lease that
owns paths is never offered a write whose boundary magus cannot check, and a
read-only lease is offered no write at all. An unbound session, a person or an
orchestrator, gets the full set.

The file is append-only, bounded, and keyed the way the advisory markers are, so
it is a recency window rather than a history. `magus session load` is what turns
it into one: it joins the journal onto the transcript by time and stamps two
fields on each call, the hint ids that call's RESULT served and the ids its
COMMAND took up from a result served before it. Both come from the join; no host
reports either.

The join has to happen at load time because a command's text never reaches the
store. `session load` re-judges a command and keeps the program, the verdict, the
rule and a digest, and nothing else, so a later reader cannot tell `magus explain`
from `magus refs` and could never say a suggestion was taken.

Read it back per hint id:

```sh
magus session hints
```

```text
HINT           SERVED  FOLLOWED  REJECTED  REFLEX  RATE
explain-path   2       1         1         1       50.0%
query-explain  4       1         3         4       25.0%
query-path     4       0         4         4       0.0%
```

Followed means the command ran within the next five calls of the same session.
Rejected means another magus verb ran instead, and reflex means the same command
was repeated; the three describe different servings and do not sum to
served.

The output names a floor. A hint below it is spending context on advice nobody
takes and is better deleted than reworded, which is a decision for a person to
make: this command reports and changes nothing.

## The contract

An adapter emits one JSON object per line on stdout and pipes it into
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
  "agent": {
    "model": "<the model that produced this event, or null>",
    "host_version": "<the host's own version, or null>"
  },
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
- `agent.model` and `agent.host_version` are opaque strings, compared for change
  only. magus ships no model names, so nothing here lockstep-couples a magus
  release to a model. They travel as one value because they describe one thing,
  the agent that produced the event. `session show` and `session ls -o json`
  report the pair off the NEWEST event that named one, which is what a session
  ended on, and an omitted `agent` (or a null field inside it) where a host's
  record does not carry it.

## Session load across hosts

Every adapter emits the same contract. What differs is what its host's log
records, and a host that supplies less declares less.

| host        | commands | exit | skills | hook output | spawn | session id | model | host version |
| ----------- | -------- | ---- | ------ | ----------- | ----- | ---------- | ----- | ------------ |
| Claude Code | yes      | none | yes    | yes         | yes   | yes        | yes   | yes          |
| Codex       | yes      | none | none   | none        | yes   | yes        | none  | none         |
| OpenCode    | yes      | yes  | yes    | none        | none  | yes        | none  | none         |

A report reads this table and says **unobservable** for a `none`, never zero.
Zero is a measurement; unobservable is the absence of one, and collapsing the two
turns a host with a thinner log into a host whose agents look better behaved.

The table is not prose. Each adapter carries the same statement in a line the
build reads:

```sh
grep magus-session-coverage magus-session-load-claude-code.buzz
```

```text
// magus-session-coverage: schema=2 host=claude-code commands=yes exit=none skills=yes hook-output=yes spawn=yes session-id=yes model=yes host-version=yes
```

An adapter that drops a dimension fails the build, and so does a table cell that
disagrees with one. Silence is the bug: an undeclared dimension is one nobody was
asked about.

## One caveat that is not a coverage gap

Claude Code writes a hook record only when the hook produced OUTPUT. A guard that
passed silently and a guard that was never wired leave the same nothing behind.
Measured over 98,233 Bash calls in one 21-day window: 14,257 carried a hook
record, and not one of the 12,012 successes carried an empty one. So an adapter
emits what is there and never infers absence, and "this command was unguarded" is
a verdict for the join against magus's trail, not for the extraction.

## Run an adapter

```sh
magus buzz -s magus-session-load-claude-code.buzz               # extract and load
magus buzz -s magus-session-load-claude-code.buzz -- --stdout   # read the stream yourself
```

Each adapter is Buzz, run by `magus buzz`, so it needs neither a POSIX shell nor
`jq`.

Declare it instead, and you stop running it by hand:

```yaml
knowledge:
  sessions:
    adapters:
      - host: claude-code
        command: [magus, buzz, -s, magus-session-load-claude-code.buzz]
```

`magus graph build` runs each declared adapter before it assembles, so the
overlay is rebuilt from the same command that rebuilds everything else reading
it, and the daemon's `sync-graph` job carries it on the daemon's own schedule
with nothing further to set up. `--no-sessions` skips them for one build;
`knowledge.sessions.disabled` turns them off for good. An adapter that fails is
reported and not fatal: the graph is then missing its newest sessions, which is a
smaller problem than no graph.

Nothing is derived. An adapter reads a transcript store under your home
directory, and magus does not go looking through it because a config key was left
blank.

Each one scopes to a repository (`HOST_REPO_ROOT`, defaulting to the active
workspace of the current directory) and keeps a per-file checkpoint under
`${XDG_STATE_HOME:-~/.local/state}/magus/session-load/<host>/`, so a re-run reads
only what is new. The checkpoint moves only after the whole stream is delivered:
a failed load is retried, never skipped. `--stdout` delivers it to you, so it
moves the checkpoint too; to look without consuming, point `SESSION_STATE_DIR` at
a scratch directory for that run.

Each file's header lists the variables it takes. Every one of them announces
itself on stderr when it cannot run, rather than exiting quietly, because an
adapter that extracted nothing looks exactly like a host nobody used.

## Checking whether your copy is current

The adapters carry the same version marker the guard templates do, for the same
reason: once you copy one it is yours, magus cannot reach it again, and nothing
about your copy says how old it is.

```sh
grep magus-guard-template magus-session-load-claude-code.buzz
```

## Claude Code

Sessions are JSONL under `~/.claude/projects/<encoded-cwd>/`, with subagent
transcripts a level down under `<sessionId>/subagents/`. Both are read: excluded,
the delegated half of every fanned-out session goes with them.

```buzz
// magus session load adapter: turns Claude Code's session store into the magus
// session event contract, one JSON object per line.
//
// This file is the source of truth. The docs site embeds it, magus's own
// repository invokes it, and you can download it and do the same. Nothing in it is
// magus-internal: it reads the store with the magus Buzz host modules and hands the
// stream to `magus session load`.
//
// Contract, one line per event:
//
//   {"host":"claude-code","session":"<id>","ts":<unix ms>,"cwd":"<abs>",
//    "kind":"shell.command|file.read|file.write|skill.load|hook.output|spawn|magus.call",
//    "ref":"<the host's own id for this event>","text":"<command | path | skill | hook text>",
//    "transcript":"<abs>",
//    "agent":{"model":"<message.model, or null>","host_version":"<version, or null>"},
//    "outcome":{"exit":null,"denied":false,"interrupted":false}}
//
// Run it as `magus buzz -s magus-session-load-claude-code.buzz` to pipe the stream
// into `magus session load`; add `-- --stdout` to read the stream yourself. Override
// any of:
//
//   HOST_REPO_ROOT      the Magus workspace to scope to; default is the active
//                       workspace of the current directory. A cwd UNDER it counts,
//                       which keeps nested project sessions in
//   HOST_SESSION_STORE  where Claude Code keeps its sessions
//   HOST_PROJECT_DIRS   the directories to walk, space separated. Default is
//                       every project directory under the store whose name
//                       begins with the encoded repo root
//   SESSION_STATE_DIR   where the per-file offsets live
//   SESSION_MAGUS_BIN   path to the binary, when it is not on PATH
//
// The `text` of a spawn is the SUBAGENT TYPE, not the prompt the host records.
// A prompt is the delegating agent's own words about work in progress, it is
// unbounded, and the audit question it would answer ("what was this agent told")
// is not one any report here asks. The type answers the one that IS asked: which
// kind of agent ran, how often, and what it did next.
//
// Re-runs are incremental: each transcript's consumed byte count is checkpointed
// under SESSION_STATE_DIR, and the checkpoint is written only after the whole
// stream is delivered, so a failed load is retried rather than skipped. Byte
// offsets stop at the last COMPLETE line, because the host appends to a file this
// script is reading.
//
// The line below declares, per dimension of the contract, what this host can
// supply: yes when the store carries it, none when it does not. It is machine-read
// by the session-parity gate, which fails the build when an adapter drops a
// dimension or the guide's table disagrees with it. A host that supplies less
// declares less; the report then says unobservable rather than zero.
// magus-guard-template: 21
// magus-session-coverage: schema=2 host=claude-code commands=yes exit=none skills=yes hook-output=yes spawn=yes session-id=yes model=yes host-version=yes

// EVERY call that can fail is caught. A failure is a transcript this run does not
// read, not a reason to abandon the ones it can: a single malformed line would
// otherwise end the walk and leave every later session unloaded.

import "std";
import "io";
import "env";
import "flags";
import "fs";
import "proc";
import "crypto";
import "time";
import "strings";
import "encoding/json";
import "lib/hook" as hook;

final HOST = "claude-code";
final STDOUT_FLAG = "--stdout";

// warn says why nothing was extracted. An adapter that loaded nothing looks exactly
// like a host nobody used, so every arm that gives up announces itself on stderr.
fun warn(message: str) > void {
    io\stderr.write("magus session load: {message}\n") catch void;
}

object Event {
    session: str,
    ts: int,
    cwd: str,
    kind: str,
    ref: str,
    text: str,
    model: any?,
    hostVersion: any?,
    denied: bool,
    interrupted: bool,
}

// render writes the contract's fields in the contract's order, which a map would not.
fun render(e: Event, transcript: str) > str {
    fun s(v: any?) > str { return json\stringify(v) catch "null"; }
    return "\{\"host\":{s(HOST)},\"session\":{s(e.session)},\"ts\":{e.ts},\"cwd\":{s(e.cwd)},"
        + "\"kind\":{s(e.kind)},\"ref\":{s(e.ref)},\"text\":{s(e.text)},\"transcript\":{s(transcript)},"
        + "\"agent\":\{\"model\":{s(e.model)},\"host_version\":{s(e.hostVersion)}},"
        + "\"outcome\":\{\"exit\":null,\"denied\":{e.denied},\"interrupted\":{e.interrupted}}}";
}

// ms is an RFC 3339 timestamp as Unix milliseconds, 0 when it does not parse.
fun ms(stamp: str) > int {
    final parsed = time\parse("2006-01-02T15:04:05Z07:00", value: stamp) catch -1.0;
    if (parsed < 0.0) { return 0; }
    return std\toInt(parsed);
}

fun toolKind(name: str) > str? {
    if (name == "Bash") { return "shell.command"; }
    if (name == "Read" or name == "NotebookRead") { return "file.read"; }
    if (name == "Edit" or name == "Write" or name == "MultiEdit" or name == "NotebookEdit") { return "file.write"; }
    if (name == "Skill") { return "skill.load"; }
    if (name == "Agent" or name == "Task") { return "spawn"; }
    if (name.startsWith("mcp__magus")) { return "magus.call"; }
    return null;
}

fun toolText(name: str, input: any?) > str {
    if (name == "Bash") { return hook\field(input, dotPath: "command"); }
    if (name == "Skill") {
        final skill = hook\field(input, dotPath: "skill");
        if (skill != "") { return skill; }
        return hook\field(input, dotPath: "name");
    }
    if (name == "Agent" or name == "Task") { return hook\field(input, dotPath: "subagent_type"); }
    if (name.startsWith("mcp__magus")) { return name; }
    return hook\field(input, dotPath: "file_path");
}

fun newEvent(r: any?, kind: str, ref: str, text: str) > Event {
    return Event{
        session = hook\field(r, dotPath: "sessionId"),
        ts = ms(hook\field(r, dotPath: "timestamp")),
        cwd = hook\field(r, dotPath: "cwd"),
        kind = kind,
        ref = ref,
        text = text,
        model = hook\dig(r, dotPath: "message.model"),
        hostVersion = hook\dig(r, dotPath: "version"),
        denied = false,
        interrupted = false,
    };
}

fun listAt(r: any?, dotPath: str) > [any] {
    return (hook\dig(r, dotPath: dotPath) as? [any]) ?? [<any>];
}

fun inScope(cwd: str, root: str) > bool {
    return cwd == root or cwd.startsWith(root + "/");
}

// extract reads one transcript's unread records and returns contract lines.
//
// Claude Code records a tool_use and its result as two records, so an event is
// parked under its id and released when the result arrives, carrying the outcome
// back to the call it belongs to. Whatever is still parked at the end of the chunk
// is released too, which is how the last call of an incremental chunk is emitted
// rather than lost.
fun extract(lines: [str], transcript: str, root: str) > [str] {
    final emitted = mut [<str>];
    final parked = mut {<str: Event>};
    final order = mut [<str>];
    foreach (line in lines) {
        if (line == "") { continue; }
        final r = json\parse(line) catch null;
        if (r == null) { continue; }
        if (!inScope(hook\field(r, dotPath: "cwd"), root: root)) { continue; }
        final kind = hook\field(r, dotPath: "type");
        if (kind == "assistant") {
            foreach (item in listAt(r, dotPath: "message.content")) {
                if (hook\field(item, dotPath: "type") != "tool_use") { continue; }
                final name = hook\field(item, dotPath: "name");
                final eventKind = toolKind(name);
                if (eventKind == null) { continue; }
                final id = hook\field(item, dotPath: "id");
                if (parked[id] == null) { order.append(id); }
                parked[id] = newEvent(r, kind: eventKind!, ref: id, text: toolText(name, input: hook\dig(item, dotPath: "input")));
            }
        } else if (kind == "user") {
            foreach (item in listAt(r, dotPath: "message.content")) {
                if (hook\field(item, dotPath: "type") != "tool_result") { continue; }
                final id = hook\field(item, dotPath: "tool_use_id");
                final e = parked[id];
                if (e == null) { continue; }
                final interrupted = (hook\dig(r, dotPath: "toolUseResult.interrupted") as? bool) ?? false;
                emitted.append(render(Event{
                    session = e!.session, ts = e!.ts, cwd = e!.cwd, kind = e!.kind, ref = e!.ref,
                    text = e!.text, model = e!.model, hostVersion = e!.hostVersion,
                    denied = hook\dig(r, dotPath: "toolDenialKind") != null,
                    interrupted = interrupted,
                }, transcript: transcript));
                parked.remove(id);
            }
        } else if (kind == "attachment" and hook\field(r, dotPath: "attachment.type").startsWith("hook_")) {
            var ref = hook\field(r, dotPath: "attachment.toolUseID");
            if (ref == "") { ref = hook\field(r, dotPath: "uuid"); }
            var text = hook\field(r, dotPath: "attachment.content");
            if (text == "") { text = hook\field(r, dotPath: "attachment.stdout"); }
            emitted.append(render(newEvent(r, kind: "hook.output", ref: ref, text: text), transcript: transcript));
        }
    }
    foreach (id in order) {
        final e = parked[id];
        if (e != null) { emitted.append(render(e!, transcript: transcript)); }
    }
    return emitted;
}

// wholeLines is the prefix of chunk that ends at its last line break. A chunk whose
// last byte is not one is a host mid-append, and the fragment after the break is
// left for the next run to read whole.
fun wholeLines(chunk: str) > str {
    var end = chunk.len();
    while (end > 0) {
        final b = chunk.byte(end - 1) catch -1;
        if (b == 10) { break; }
        end = end - 1;
    }
    return chunk.sub(0, len: end);
}

fun parseOffset(mark: str) > int {
    final text = (fs\readFile(mark) catch "").trim();
    if (text == "") { return 0; }
    return std\parseInt(text) ?? 0;
}

// encodeRoot names a project directory the way Claude Code does: every byte outside
// [A-Za-z0-9] becomes a dash. Matching on the encoded root as a PREFIX is what picks
// up worktrees, whose paths extend the root.
fun encodeRoot(root: str) > str {
    final encoded = mut [<str>];
    foreach (i in 0..root.len()) {
        final b = root.byte(i) catch 0;
        final ok = (b >= 48 and b <= 57) or (b >= 65 and b <= 90) or (b >= 97 and b <= 122);
        if (ok) { encoded.append(root.sub(i, len: 1)); } else { encoded.append("-"); }
    }
    return encoded.join("");
}

fun projectDirs(store: str, root: str) > [str] {
    final declared = env\get("HOST_PROJECT_DIRS") catch "";
    if (declared != "") { return strings\fields(declared); }
    final prefix = encodeRoot(root);
    final dirs = mut [<str>];
    foreach (name in fs\listDir(store) catch [<str>]) {
        if (!name.startsWith(prefix)) { continue; }
        final dir = "{store}/{name}";
        if (fs\isDir(dir) catch false) { dirs.append(dir); }
    }
    return dirs;
}

// transcripts lists every JSONL file under dir. Subagent transcripts sit a level
// down, under <sessionId>/subagents/, and carry the orchestrator's session id;
// excluded, they take the delegated half of every fanned-out session with them.
fun transcripts(dir: str) > [str] {
    final found = mut [<str>];
    fs\walk(dir, callback: fun (path: str, isDir: bool) > bool {
        if (!isDir and path.endsWith(".jsonl")) { found.append(path); }
        return false;
    }) catch void;
    return found;
}

fun repoRoot(bin: str) > str {
    final declared = env\get("HOST_REPO_ROOT") catch "";
    if (declared != "" or bin == "" or !hook\isExecutable(bin)) { return declared; }
    final result = proc\exec(bin, args: ["describe", "projects", "-o", "template=\{\{.workspace}}"], opts: {
        "quiet": true,
        "allow_failure": true,
    }) catch null;
    if (result == null or result!.code != 0) { return ""; }
    return hook\trimTrailingNewlines(result!.stdout);
}

fun main(args: [str]) > int {
    final parsed = flags\parse(args, switches: [STDOUT_FLAG], valued: [<str>]) catch null;
    final toStdout = parsed != null and parsed!.values[STDOUT_FLAG] != null;
    final bin = hook\envOr("SESSION_MAGUS_BIN", fallback: hook\resolveBin());
    final root = repoRoot(bin);
    if (root == "") {
        warn("no Magus workspace here, so there is nothing to scope events to. Run this inside a workspace, or set HOST_REPO_ROOT.");
        return 0;
    }
    if (!toStdout and (bin == "" or !hook\isExecutable(bin))) {
        warn("magus is not on PATH, so the extracted events were dropped rather than loaded. Set SESSION_MAGUS_BIN, or pass --stdout to read the stream yourself.");
        return 0;
    }

    final home = env\get("HOME") catch "";
    final store = hook\envOr("HOST_SESSION_STORE", fallback: "{home}/.claude/projects");
    final stateHome = hook\envOr("XDG_STATE_HOME", fallback: "{home}/.local/state");
    final stateDir = hook\envOr("SESSION_STATE_DIR", fallback: "{stateHome}/magus/session-load/{HOST}");

    final events = mut [<str>];
    final marks = mut {<str: int>};
    foreach (dir in projectDirs(store, root: root)) {
        foreach (transcript in transcripts(dir)) {
            final mark = "{stateDir}/{crypto\sha256Hex(transcript).sub(0, len: 16)}";
            final content = fs\readFile(transcript) catch null;
            if (content == null) { continue; }
            var offset = parseOffset(mark);
            // A file smaller than its checkpoint was rotated or replaced, so the offset
            // describes bytes that no longer exist and reading from it would land mid-record.
            if (content!.len() < offset) { offset = 0; }
            if (content!.len() <= offset) { continue; }
            final whole = wholeLines(content!.sub(offset));
            if (whole == "") { continue; }
            foreach (line in extract(whole.split("\n"), transcript: transcript, root: root)) {
                events.append(line);
            }
            marks[mark] = offset + whole.len();
        }
    }

    var stream = events.join("\n");
    if (events.len() > 0) { stream = stream + "\n"; }
    if (toStdout) {
        io\stdout.write(stream) catch void;
    } else {
        final loaded = proc\exec(bin, args: ["session", "load"], opts: {"stdin": stream, "allow_failure": true}) catch null;
        if (loaded == null or loaded!.code != 0) { return 1; }
    }

    // Checkpoints are committed only once the stream has been delivered. A load that
    // failed leaves every offset where it was, so the retry re-reads the same records
    // instead of the audit quietly losing them.
    fs\mkdirAll(stateDir) catch void;
    foreach (mark, position in marks) {
        fs\writeFile(mark, content: "{position}") catch void;
    }
    return 0;
}
```

## Codex

Rollouts are JSONL under `~/.codex/sessions/YYYY/MM/DD/`. Nothing names them
after a repository, so each is opened and scoped from the `session_meta` record
inside it. Codex records no skill loads and no hook output, and its only
exit-like signal describes a patch rather than a command.

```buzz
// magus session load adapter: turns Codex's rollout files into the magus session
// event contract, one JSON object per line.
//
// This file is the source of truth. The docs site embeds it, and you can download
// it and run it yourself. Its Claude Code sibling carries the full contract; the
// fields are identical, less the agent object this host cannot fill.
//
// Run it as `magus buzz -s magus-session-load-codex.buzz` to pipe the stream into
// `magus session load`; add `-- --stdout` to read the stream yourself. Override any
// of:
//
//   HOST_REPO_ROOT      the Magus workspace to scope to; default is the active
//                       workspace of the current directory. A cwd UNDER it counts,
//                       which keeps nested project sessions in
//   HOST_SESSION_STORE  where Codex keeps its rollout files
//   SESSION_STATE_DIR   where the per-file offsets live
//   SESSION_MAGUS_BIN   path to the binary, when it is not on PATH
//
// Codex records no skill loads and no hook output, and its only exit-like signal
// is patch_apply_end's success flag, which describes a patch rather than a
// command. Neither session_meta nor a turn record carries a model name or a CLI
// version this adapter can point at with confidence, so both are declared none
// rather than guessed. The coverage line says so, and a report reading it says
// unobservable for those dimensions rather than zero. Declaring commands=yes on
// the strength of what the other hosts supply is the failure this line exists
// to prevent.
// magus-guard-template: 21
// magus-session-coverage: schema=2 host=codex commands=yes exit=none skills=none hook-output=none spawn=yes session-id=yes model=none host-version=none

// EVERY call that can fail is caught: a rollout this run cannot read is not a
// reason to abandon the rest.

import "std";
import "io";
import "env";
import "flags";
import "fs";
import "proc";
import "crypto";
import "time";
import "encoding/json";
import "lib/hook" as hook;

final HOST = "codex";
final STDOUT_FLAG = "--stdout";

// warn says why nothing was extracted; see the Claude Code adapter.
fun warn(message: str) > void {
    io\stderr.write("magus session load: {message}\n") catch void;
}

// render writes the contract's fields in the contract's order, which a map would not.
fun render(session: str, ts: int, cwd: str, kind: str, ref: str, text: str, transcript: str) > str {
    fun s(v: any?) > str { return json\stringify(v) catch "null"; }
    return "\{\"host\":{s(HOST)},\"session\":{s(session)},\"ts\":{ts},\"cwd\":{s(cwd)},"
        + "\"kind\":{s(kind)},\"ref\":{s(ref)},\"text\":{s(text)},\"transcript\":{s(transcript)},"
        + "\"outcome\":\{\"exit\":null,\"denied\":false,\"interrupted\":false}}";
}

// ms is an RFC 3339 timestamp as Unix milliseconds, 0 when it does not parse.
fun ms(stamp: str) > int {
    final parsed = time\parse("2006-01-02T15:04:05Z07:00", value: stamp) catch -1.0;
    if (parsed < 0.0) { return 0; }
    return std\toInt(parsed);
}

fun inScope(cwd: str, root: str) > bool {
    return cwd == root or cwd.startsWith(root + "/");
}

// extract reads one rollout and returns contract lines.
//
// Codex nests rollouts by date and names none of them after the repository, so
// every file is opened and scoped from the session_meta record inside it. The
// session id and cwd arrive on that first record and are carried forward, which is
// why this folds state rather than mapping each line independently.
fun extract(lines: [str], transcript: str, root: str) > [str] {
    final emitted = mut [<str>];
    var session = "";
    var cwd = "";
    foreach (line in lines) {
        if (line == "") { continue; }
        final r = json\parse(line) catch null;
        if (r == null) { continue; }
        final kind = hook\field(r, dotPath: "type");
        if (kind == "session_meta") {
            session = hook\field(r, dotPath: "payload.session_id");
            cwd = hook\field(r, dotPath: "payload.cwd");
            continue;
        }
        if (!inScope(cwd, root: root)) { continue; }
        final ts = ms(hook\field(r, dotPath: "timestamp"));
        if (kind == "custom_tool_call" and hook\field(r, dotPath: "payload.name") == "exec") {
            final input = hook\dig(r, dotPath: "payload.input");
            var text = hook\field(r, dotPath: "payload.input");
            if (input is {str: any}) { text = hook\field(input, dotPath: "command"); }
            emitted.append(render(session, ts: ts, cwd: cwd, kind: "shell.command",
                ref: hook\field(r, dotPath: "payload.call_id"), text: text, transcript: transcript));
        } else if (kind == "spawn_agent") {
            var ref = hook\field(r, dotPath: "payload.call_id");
            if (ref == "") { ref = hook\field(r, dotPath: "payload.agent_id"); }
            emitted.append(render(session, ts: ts, cwd: cwd, kind: "spawn",
                ref: ref, text: hook\field(r, dotPath: "payload.name"), transcript: transcript));
        }
    }
    return emitted;
}

// wholeLines is the prefix of chunk that ends at its last line break, so a record
// the host is still appending is read whole on the next run.
fun wholeLines(chunk: str) > str {
    var end = chunk.len();
    while (end > 0) {
        final b = chunk.byte(end - 1) catch -1;
        if (b == 10) { break; }
        end = end - 1;
    }
    return chunk.sub(0, len: end);
}

fun parseOffset(mark: str) > int {
    final text = (fs\readFile(mark) catch "").trim();
    if (text == "") { return 0; }
    return std\parseInt(text) ?? 0;
}

fun rollouts(store: str) > [str] {
    final found = mut [<str>];
    fs\walk(store, callback: fun (path: str, isDir: bool) > bool {
        if (!isDir and fs\basename(path).startsWith("rollout-") and path.endsWith(".jsonl")) { found.append(path); }
        return false;
    }) catch void;
    return found;
}

fun repoRoot(bin: str) > str {
    final declared = env\get("HOST_REPO_ROOT") catch "";
    if (declared != "" or bin == "" or !hook\isExecutable(bin)) { return declared; }
    final result = proc\exec(bin, args: ["describe", "projects", "-o", "template=\{\{.workspace}}"], opts: {
        "quiet": true,
        "allow_failure": true,
    }) catch null;
    if (result == null or result!.code != 0) { return ""; }
    return hook\trimTrailingNewlines(result!.stdout);
}

fun main(args: [str]) > int {
    final parsed = flags\parse(args, switches: [STDOUT_FLAG], valued: [<str>]) catch null;
    final toStdout = parsed != null and parsed!.values[STDOUT_FLAG] != null;
    final bin = hook\envOr("SESSION_MAGUS_BIN", fallback: hook\resolveBin());
    final root = repoRoot(bin);
    if (root == "") {
        warn("no Magus workspace here, so there is nothing to scope events to. Run this inside a workspace, or set HOST_REPO_ROOT.");
        return 0;
    }
    if (!toStdout and (bin == "" or !hook\isExecutable(bin))) {
        warn("magus is not on PATH, so the extracted events were dropped rather than loaded. Set SESSION_MAGUS_BIN, or pass --stdout to read the stream yourself.");
        return 0;
    }

    final home = env\get("HOME") catch "";
    final store = hook\envOr("HOST_SESSION_STORE", fallback: "{home}/.codex/sessions");
    final stateHome = hook\envOr("XDG_STATE_HOME", fallback: "{home}/.local/state");
    final stateDir = hook\envOr("SESSION_STATE_DIR", fallback: "{stateHome}/magus/session-load/{HOST}");

    final events = mut [<str>];
    final marks = mut {<str: int>};
    foreach (transcript in rollouts(store)) {
        final mark = "{stateDir}/{crypto\sha256Hex(transcript).sub(0, len: 16)}";
        final content = fs\readFile(transcript) catch null;
        if (content == null) { continue; }
        var offset = parseOffset(mark);
        if (content!.len() < offset) { offset = 0; }
        if (content!.len() <= offset) { continue; }

        // The whole file, never the unread tail: session_meta is the FIRST record and
        // carries the session id and cwd every later record is scoped by, so a chunk
        // starting after it has nothing to scope. Re-reading is cheap here because a
        // rollout closes when its session ends, and only the open one grows.
        final whole = wholeLines(content!);
        if (whole.len() <= offset) { continue; }

        // Emitting from byte 0 every time and letting `session load` dedup on
        // (host, session, kind, ref) is the trade this host's format forces. The
        // checkpoint still earns its place: a rollout whose size has not moved is
        // skipped entirely, which is every closed session after the first run.
        foreach (line in extract(whole.split("\n"), transcript: transcript, root: root)) {
            events.append(line);
        }
        marks[mark] = whole.len();
    }

    var stream = events.join("\n");
    if (events.len() > 0) { stream = stream + "\n"; }
    if (toStdout) {
        io\stdout.write(stream) catch void;
    } else {
        final loaded = proc\exec(bin, args: ["session", "load"], opts: {"stdin": stream, "allow_failure": true}) catch null;
        if (loaded == null or loaded!.code != 0) { return 1; }
    }

    // Committed only once the stream has been delivered, so a failed load is retried.
    fs\mkdirAll(stateDir) catch void;
    foreach (mark, position in marks) {
        fs\writeFile(mark, content: "{position}") catch void;
    }
    return 0;
}
```

## OpenCode

`opencode export <sessionID>` is documented output, so this adapter reads a
contract rather than a store. OpenCode is the only one of the three that records
a command's exit code, and the only one with no hook records and no spawn part.

```buzz
// magus session load adapter: turns `opencode export` output into the magus
// session event contract, one JSON object per line.
//
// This file is the source of truth. The docs site embeds it, and you can download
// it and run it yourself. Its Claude Code sibling carries the full contract; the
// fields are identical, less the agent object this host cannot fill.
//
// Run it as `magus buzz -s magus-session-load-opencode.buzz` to pipe the stream into
// `magus session load`; add `-- --stdout` to read the stream yourself. Override any
// of:
//
//   HOST_REPO_ROOT      the Magus workspace to scope to; default is the active
//                       workspace of the current directory. A cwd UNDER it counts,
//                       which keeps nested project sessions in
//   HOST_SESSION_IDS    the sessions to export, space separated. Default is every
//                       id `opencode sessions` lists
//   HOST_OPENCODE_BIN   path to the opencode binary, when it is not on PATH
//   SESSION_STATE_DIR   where the per-session part counts live
//   SESSION_MAGUS_BIN   path to the magus binary, when it is not on PATH
//
// This one reads a CONTRACT rather than a store: `opencode export` is documented
// output, where the other two hosts' files are de-facto shapes versioned per
// record. So it runs the export per session instead of walking a directory, and
// there is no partial-line handling to do: each export is one complete document.
//
// OpenCode is the only host of the three that records a command's exit code, and
// the only one with neither hook records nor a spawn part. The coverage line says
// both; a report reading it says unobservable, never zero. An export part carries
// no CLI version and this adapter does not read a per-part model id with enough
// confidence to publish it, so both are declared none rather than guessed.
// magus-guard-template: 21
// magus-session-coverage: schema=2 host=opencode commands=yes exit=yes skills=yes hook-output=none spawn=none session-id=yes model=none host-version=none

// EVERY call that can fail is caught: a session whose export fails is not a reason
// to abandon the rest.

import "std";
import "io";
import "env";
import "flags";
import "fs";
import "proc";
import "crypto";
import "strings";
import "encoding/json";
import "lib/hook" as hook;

final HOST = "opencode";
final STDOUT_FLAG = "--stdout";

// warn says why nothing was extracted; see the Claude Code adapter.
fun warn(message: str) > void {
    io\stderr.write("magus session load: {message}\n") catch void;
}

fun inScope(cwd: str, root: str) > bool {
    return cwd == root or cwd.startsWith(root + "/");
}

fun firstValue(node: any?, dotPaths: [str]) > str {
    foreach (dotPath in dotPaths) {
        final value = hook\field(node, dotPath: dotPath);
        if (value != "") { return value; }
    }
    return "";
}

fun toolKind(tool: str) > str? {
    if (tool == "bash") { return "shell.command"; }
    if (tool == "skill") { return "skill.load"; }
    if (tool == "read") { return "file.read"; }
    if (tool == "write" or tool == "edit" or tool == "patch") { return "file.write"; }
    return null;
}

fun toolText(tool: str, part: any?) > str {
    if (tool == "bash") { return hook\field(part, dotPath: "state.input.command"); }
    if (tool == "skill") { return hook\field(part, dotPath: "state.input.name"); }
    return firstValue(part, dotPaths: ["state.input.filePath", "state.input.path"]);
}

// toolParts is every part that records a tool call, in export order: the top-level
// parts when the export has any, the parts of each message otherwise.
fun toolParts(doc: any?) > [any] {
    var parts = (hook\dig(doc, dotPath: "parts") as? [any]) ?? [<any>];
    if (parts.len() == 0) {
        final nested = mut [<any>];
        foreach (message in (hook\dig(doc, dotPath: "messages") as? [any]) ?? [<any>]) {
            foreach (part in (hook\dig(message, dotPath: "parts") as? [any]) ?? [<any>]) {
                nested.append(part);
            }
        }
        parts = nested;
    }
    final calls = mut [<any>];
    foreach (part in parts) {
        if (hook\dig(part, dotPath: "callID") != null) { calls.append(part); }
    }
    return calls;
}

// startMillis is the part's start time in Unix milliseconds, floored, 0 when absent.
fun startMillis(part: any?) > int {
    final start = (hook\dig(part, dotPath: "state.time.start") as? double)
        ?? (hook\dig(part, dotPath: "time.start") as? double)
        ?? 0.0;
    return std\toInt(start);
}

// render writes the contract's fields in the contract's order, which a map would not.
fun render(session: str, cwd: str, part: any?, kind: str, transcript: str) > str {
    fun s(v: any?) > str { return json\stringify(v) catch "null"; }
    return "\{\"host\":{s(HOST)},\"session\":{s(session)},\"ts\":{startMillis(part)},\"cwd\":{s(cwd)},"
        + "\"kind\":{s(kind)},\"ref\":{s(hook\field(part, dotPath: "callID"))},"
        + "\"text\":{s(toolText(hook\field(part, dotPath: "tool"), part: part))},\"transcript\":{s(transcript)},"
        + "\"outcome\":\{\"exit\":{s(hook\dig(part, dotPath: "state.metadata.exit"))},"
        + "\"denied\":{hook\field(part, dotPath: "state.status") == "error"},\"interrupted\":false}}";
}

// extract returns the contract lines for the tool parts past the first skip.
fun extract(doc: any?, parts: [any], skip: int, transcript: str, root: str) > [str] {
    final emitted = mut [<str>];
    final cwd = firstValue(doc, dotPaths: ["session.directory", "directory"]);
    if (!inScope(cwd, root: root)) { return emitted; }
    final session = firstValue(doc, dotPaths: ["session.id", "id"]);
    foreach (i, part in parts) {
        if (i < skip) { continue; }
        final kind = toolKind(hook\field(part, dotPath: "tool"));
        if (kind == null) { continue; }
        emitted.append(render(session, cwd: cwd, part: part, kind: kind!, transcript: transcript));
    }
    return emitted;
}

fun parseSkip(mark: str) > int {
    final text = (fs\readFile(mark) catch "").trim();
    if (text == "") { return 0; }
    return std\parseInt(text) ?? 0;
}

fun sessionIds(opencode: str) > [str] {
    final declared = env\get("HOST_SESSION_IDS") catch "";
    if (declared != "") { return strings\fields(declared); }
    final listed = proc\exec(opencode, args: ["sessions", "--json"], opts: {"quiet": true, "allow_failure": true}) catch null;
    if (listed == null or listed!.code != 0) { return [<str>]; }
    final ids = mut [<str>];
    final listing = json\parse(listed!.stdout) catch null;
    foreach (entry in (listing as? [any]) ?? [<any>]) {
        final id = hook\field(entry, dotPath: "id");
        if (id != "") { ids.append(id); }
    }
    return ids;
}

fun repoRoot(bin: str) > str {
    final declared = env\get("HOST_REPO_ROOT") catch "";
    if (declared != "" or bin == "" or !hook\isExecutable(bin)) { return declared; }
    final result = proc\exec(bin, args: ["describe", "projects", "-o", "template=\{\{.workspace}}"], opts: {
        "quiet": true,
        "allow_failure": true,
    }) catch null;
    if (result == null or result!.code != 0) { return ""; }
    return hook\trimTrailingNewlines(result!.stdout);
}

fun main(args: [str]) > int {
    final parsed = flags\parse(args, switches: [STDOUT_FLAG], valued: [<str>]) catch null;
    final toStdout = parsed != null and parsed!.values[STDOUT_FLAG] != null;
    final bin = hook\envOr("SESSION_MAGUS_BIN", fallback: hook\resolveBin());
    final root = repoRoot(bin);
    if (root == "") {
        warn("no Magus workspace here, so there is nothing to scope events to. Run this inside a workspace, or set HOST_REPO_ROOT.");
        return 0;
    }
    final opencode = hook\envOr("HOST_OPENCODE_BIN", fallback: proc\which("opencode") catch "");
    if (opencode == "" or !hook\isExecutable(opencode)) {
        warn("opencode is not on PATH, so no session events were extracted. Set HOST_OPENCODE_BIN to its path.");
        return 0;
    }
    if (!toStdout and (bin == "" or !hook\isExecutable(bin))) {
        warn("magus is not on PATH, so the extracted events were dropped rather than loaded. Set SESSION_MAGUS_BIN, or pass --stdout to read the stream yourself.");
        return 0;
    }

    final home = env\get("HOME") catch "";
    final stateHome = hook\envOr("XDG_STATE_HOME", fallback: "{home}/.local/state");
    final stateDir = hook\envOr("SESSION_STATE_DIR", fallback: "{stateHome}/magus/session-load/{HOST}");

    // An export is one document, so the checkpoint counts PARTS rather than bytes:
    // the first N tool parts of a session are the ones already loaded.
    final events = mut [<str>];
    final marks = mut {<str: int>};
    foreach (id in sessionIds(opencode)) {
        final mark = "{stateDir}/{crypto\sha256Hex(id).sub(0, len: 16)}";
        final exported = proc\exec(opencode, args: ["export", id], opts: {"quiet": true, "allow_failure": true}) catch null;
        if (exported == null or exported!.code != 0) { continue; }
        final doc = json\parse(exported!.stdout) catch null;
        if (doc == null) { continue; }
        final parts = toolParts(doc);
        final skip = parseSkip(mark);
        if (parts.len() <= skip) { continue; }
        foreach (line in extract(doc, parts: parts, skip: skip, transcript: "opencode://{id}", root: root)) {
            events.append(line);
        }
        marks[mark] = parts.len();
    }

    var stream = events.join("\n");
    if (events.len() > 0) { stream = stream + "\n"; }
    if (toStdout) {
        io\stdout.write(stream) catch void;
    } else {
        final loaded = proc\exec(bin, args: ["session", "load"], opts: {"stdin": stream, "allow_failure": true}) catch null;
        if (loaded == null or loaded!.code != 0) { return 1; }
    }

    // Committed only once the stream has been delivered, so a failed load is retried.
    fs\mkdirAll(stateDir) catch void;
    foreach (mark, position in marks) {
        fs\writeFile(mark, content: "{position}") catch void;
    }
    return 0;
}
```
