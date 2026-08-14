---
title: The guard
description: What magus hook denies, what it explains, and why - the four deny triggers, the file-path surface, the verdict contract a host wires into, and the observations magus records.
tags: [agents, guard, hooks, magus hook, telemetry, activity]
---

# The guard

Most agent hosts can run a hook before executing a shell command or writing a
file. magus supplies the rule evaluation; the host supplies the hook that calls
it. `magus hook` reads one command or one path, applies the rules, and returns a
neutral verdict.

Wiring is per host: [Claude Code](claude-code.md), [Codex](codex.md),
[Cursor](cursor.md), [OpenCode](opencode.md), or
[any host that can run a command](any-host.md). The rules below are the same
everywhere, because they come from one binary.

## Deny only what cannot be undone

magus explains everything else. Say that plainly, because the temptation runs
the other way: a guard that can prove something is wrong wants to block it.

A whole-tree `git reset --hard` destroys uncommitted and untracked work,
including a concurrent agent's, and nothing brings it back. magus denies that.
A hand-edited generated file only wastes your time, because regenerating erases
it, so magus explains instead - even though it knows from the target's own
declarations that the file is generated. Blocking there would treat you as
unable to learn something one `magus describe file` away. An agent told why an
edit was futile does not repeat it; an agent whose call was rejected has only
lost a turn.

## The deny triggers

magus denies a call on any one of four independent grounds.

**It cannot be undone.** The destructive whole-tree VCS operations.

**It writes into the working tree outside magus.** Codegen, a formatter with
`-w`, `--write` or `--fix`, `go mod tidy`, build output landing on a tracked
path. This is the firm one, and the only one with no judgment in it. A write
that skips magus is not merely slower: the target that owns that path now
reports drift it did not cause, the cache holds a result for a tree that no
longer exists, and affected tracking has no record that anything moved. Reading
through the wrong tool costs a cache hit; writing through the wrong tool
corrupts the workspace's account of itself.

**It has an exact working equivalent.** A raw `go test` is harmless and
reversible, so it fails the first two tests. magus denies it because the
replacement is complete, which makes the deny free. Where no equivalent exists
the rule may only advise: magus has no raw-text search, so a repo-wide `grep`
gets an explanation, and an earlier attempt to deny it was reverted. Denying
grep was wrong because the deny removed a capability with nothing to route to,
not because grep is safe.

**It breaks a provenance guarantee.** The first three judge the write - whether
it can be taken back, whether it bypassed the tool, whether it was redundant.
This one judges what the write does to the corpus: the artifact's value depends
on a guarantee about who authored it, and undoing the write does not restore the
guarantee.

Its only instance is a write into a declared notes store. A note is the one
thing in the knowledge graph that is not derived from the workspace: a doc comes
from markdown, a rationale from a comment, a symbol from an index, an author
from git, and rebuilding the graph recovers every one of them. A note's content
originates with a person, nothing in the repository corroborates it later, and
no rebuild recovers it. One agent-written note does not damage that note; it
damages a reader's ability to trust any note without checking blame, and a note
of uncertain authorship is worthless rather than merely weaker.

That trigger licenses less than it might appear. It is not "the file is
important", and it is not a general provenance rule - source files carry
authorship too, and writing them is the job. It applies only where the artifact
has no other corroboration, which is what makes authorship its entire value.

## What magus denies

The guard parses the shell rather than pattern-matching the string, so it reads
the command being RUN: an environment prefix, `env -u GOROOT ...`, a launcher,
or `bash -c '...'` all reach the same verdict as the bare command.

- **Destructive whole-tree VCS operations**: `git stash`, `git reset --hard`,
  `git checkout .`, `git restore .`, `git clean -f`. These rules are git-shaped.
  magus also drives Mercurial and Jujutsu, where recoverability differs - jj
  snapshots the working copy and keeps an operation log, so its nearest
  equivalents are undoable and would not meet this bar. Their commands are not
  matched today.
- **Raw language tools**: `go test`, `go build`, `go mod tidy`, `pytest`,
  `cargo build`, `eslint`, `ruff`, `gofmt -w`, `prettier --write`, and the rest.
  The reason names the escalation ladder: a top-level target first, then a
  single spell op (`magus run go::go-test <project>`), which still runs through
  magus, and `--dry-run` to see the exact command either would run. Read-only
  invocations pass: `gofmt -l` and `gofmt -d` report without writing, so they
  bypass nothing.
- **Staging everything**: `git add -A`, `git add .`, `git add -u`. A magus
  target writes its declared outputs as it runs, so a tree is routinely dirty
  with generated files you did not edit; sweeping them into a commit about
  something else is how a focused change becomes unreviewable.
- **Piping or redirecting magus's own output**: `| tail`, `> file`, `>> file`,
  `2>&1`. The equivalent is exact - `-o name|json|template=` returns the field
  the filter was reaching for, and every run persists its full log, so a failure
  prints that path with the ref. A pipe additionally replaces the exit status
  with the last stage's, so `magus affected ci | tail` reports tail's success
  and a failing gate reads as exit 0. `magus query output <ref>` is the one
  exemption: a raw captured tool log has no schema to project.
- **Writing into the declared notes store** (`knowledge.notes.shared`), however
  the write is spelled. A file write into the store is caught on the path
  surface; `magus notes edit` reading piped prose is a command, so it is caught
  here. The reason names both alternatives: `magus memory put` for a workspace
  decision an agent may record, and `magus notes edit` for a person to write the
  note themselves. The opt-in is the key in the repository's own `magus.yaml`, and
  the rule is armed from that moment - before the store holds a single note,
  because otherwise an agent could author its first note and the deny would
  switch on afterwards. A declaration made anywhere else (an explicit
  `--config`, user-global config) is in effect in every workspace on the
  machine, so it arms this rule only where the store already exists.
- **In-place stream edits**: `sed -i`, `sed --in-place`. The flag is not
  portable and the two spellings destroy each other's work: GNU reads
  `sed -i 's/x/y/' f` as an edit, BSD and macOS read that same script as the
  backup suffix, and the portable-looking `sed -i '' ...` makes GNU edit
  nothing. The command that worked where it was written mangles the file on the
  next machine, by writing, so the damage lands before anyone reads a diff.
  Every host driving this guard has a structured editor tool that applies an
  exact replacement and reports what changed. Reading with sed is untouched.
- **Running magus from a copy of the workspace** in a temp or scratchpad
  directory (`cd /tmp/... && magus ...`, including via a variable assigned
  earlier on the same line). The verdict would describe a tree nobody ships:
  generated files land in the copy, the cache splits, and duplicated spell
  sources trip MGS1002. To work on a different workspace, pass `--root <path>`.

## What magus explains

An advise verdict carries context your host can inject while the call proceeds.

- `git commit` and `git add <paths>`: classify the dirty tree first. Deliberate
  staging is the replacement the rule above points at, so it is never denied.
- A path-scoped `git checkout -- <paths>` or `git restore`: regenerated output
  is a declared target output, and reverting it because you did not hand-edit it
  is what makes CI fail on drift.
- A repo-wide text search (`grep -r`, `rg`, `find -name`): the graph answers
  structural questions from declared sources - `magus refs` for a code symbol,
  `magus query` for a domain entity.
- `cd <dir> && magus ...` within the workspace: magus is CWD-relative and the
  project is always an explicit argument, so the `cd` is how the right command
  lands on the wrong project. A `cd` into a temp or scratchpad copy is denied
  instead, because that one changes what the answer means rather than only where
  it runs.

Everything else passes.

## The file surface

`magus hook --path <file>` judges a file path rather than a command. Two of its
rules are definitive rather than heuristic, because both read DECLARATIONS: the
generated-output rule classifies the path against every target's declared
outputs, and the notes rule against the declared notes store. The first advises,
because a hand-edited generated file is wasteful rather than destructive; the
second denies, on the provenance trigger.

Both say nothing on any uncertainty. A rule fired on a guess trains the reader
to ignore it, and a deny fired on a guess blocks real work.

Wire this to your host's file-editing tool, not its shell tool.

## The verdict contract

The input arrives however your host can produce it: as raw text on stdin, or as
the host's own JSON event. magus reads `tool_input.command`,
`tool_input.file_path`, `session_id` and `hook_event_name` out of an envelope
directly, so a host that writes one needs neither `jq` nor `--path` - a payload
carrying a file path is judged as a write.

The verdict leaves through the standard output arm: `-o json` for a
schema-versioned envelope, `-o yaml`, `-o name` for the bare decision word, or
`-o template=<go-template>` to render your host's response dialect. Bare
`-o template` lists the fields.

```sh
printf '%s' 'git stash' | magus hook -o json
printf '%s' 'go test ./...' | magus hook -o name
printf '%s' 'MAGUS.md' | magus hook --path -o name
```

A deny exits 2 with the verdict on stdout; a pass and an advise exit 0. An
unreadable event fails open as a `pass`, because a guard that errors on every
tool call is worse than no guard.

A host integration is therefore a few lines of configuration you own, with no
host-specific code in magus.

## Not a security boundary

It reads a command string and returns an opinion. That catches a habit and does
nothing against intent. `TestGuardKnownHoles` records what it misses: a command
inside a script file, a program name from `$(...)` or a variable, a shell alias,
a recipe behind `make`.

You own the hook script and its response template. Edit them so denials stop
arriving, and you have configured your tool, the same way you can turn off every
rule in `.eslintrc`.

`magus affected ci` is the gate that holds. It is committed, it passes through
review, and no local config edit changes what it runs.

Ask where a config came from rather than who can edit it. One you wrote is
yours. One that arrived in a cloned repository is a stranger's code your host
may run - the same standing risk as that repo's `Makefile` or git hooks, and
older than agents. Read it before you run it.

## What magus records

Every `magus hook` invocation with a readable command or path appends one
`agent_command` event to the local Activity Trail. This is product telemetry for
improving agent support: which host tool an agent selected, whether it reached a
magus surface or a raw command, and which guidance would move that workflow onto
magus. It is not a security feature and never an execution gate - recording is
best effort, local, and cannot change a verdict.

The hook writes a normalized request and response as content-addressed blobs
rather than the opaque host event. The request is schema-versioned and carries
only the stable fields:

```json
{
  "schema_version": 1,
  "host": "claude-code",
  "session": "abc123",
  "event": "PreToolUse",
  "tool": "Bash",
  "command": "magus run test ."
}
```

For a file-edit hook, `path` replaces `command`. The response carries the same
schema version plus `decision` and, where applicable, `reason` or `context`.
`host` and `session` also sit on the event row itself, not only in the blob, so
a view can group a page of observations without fetching a payload per row.

No local process can discover which agent host started it, so the wrapper passes
the name in with `magus hook --agent-name`; a wrapper that does not leaves the
field empty rather than guessing. An MCP call has no wrapper to ask and is
attributed from its HTTP `User-Agent` instead.

`agent_command` means observed invocation, not successful execution. A pre-tool
hook runs before the host decides whether to call the tool, so an `OUTCOME_OK`
event means magus recorded and evaluated the observation - not that a shell
process started, exited zero, or ran at all. Direct MCP calls stay
`mcp_tool_call` events for that reason: their wrapper sees the actual result.

Events live at `<cache-dir>/activity/events.jsonl` with blobs under
`<cache-dir>/activity/blobs`, under the trail's existing bounded retention
(10,000 newest events, unreferenced blobs collected on rotate). magus keeps the
commands and paths themselves, because they are the evidence that shows where
adoption breaks down, so keep credentials out of a command line. Inspect them
through the authenticated Activity view. There is no network exporter, no
scoring system, and no instrumentation inside a magus target or a Buzz execution
path.

A host without a hook cannot be observed: no local CLI can discover commands
another process did not report. The coverage boundary is explicit rather than
guessed.
