---
title: The guard
description: What magus session hook denies, what it explains, and why - the four deny triggers, the file-path surface, the verdict contract a host wires into, and the observations magus records.
tags: [agents, guard, hooks, magus session hook, telemetry, activity]
---

# The guard

Most agent hosts can run a hook before executing a shell command or writing a
file. magus supplies the rule evaluation; the host supplies the hook that calls
it. `magus session hook` reads one command or one path, applies the rules, and returns a
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

Its instances are a write into a declared notes store and a read receipt an
agent mints for itself. Both refuse an agent authoring a human's statement, and
the notes store is the one worth reading out in full. A note is the one
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
  `git checkout .`, `git restore .`, `git clean -f`, and `git worktree remove`,
  which destroys another tree's uncommitted and untracked work rather than this
  one's - in a repository running several checkouts that is routinely another
  session's, and it is in no commit to recover from. Reading a stash is exempt
  (`git stash list`, `git stash show`), as is `git stash create`, which returns a
  commit object without touching the working tree or the stash stack.
  These rules are git-shaped.
  magus also drives Mercurial and Jujutsu, where recoverability differs - jj
  snapshots the working copy and keeps an operation log, so its nearest
  equivalents are undoable and would not meet this bar. Their commands are not
  matched today.
- **Raw language tools**: `go test`, `go build`, `go mod tidy`, `cargo build`,
  `gofmt -w`, `prettier --write`, and the rest. The match is the base PROGRAM a
  registered spell op renders plus the leading argv it renders with it, so the
  denied spelling is the one a spell would actually launch. A tool a spell
  reaches through a runner is therefore matched under the runner:
  `uv run pytest` and `pnpm exec eslint .` deny, while bare `pytest`, `eslint`
  and `ruff` pass, because no spell renders those as the program. That is
  silence rather than endorsement - a target still covers the work.
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
- **Minting a read receipt** (`magus diff --ack`). A receipt records that a
  PERSON read a change, so there is no spelling of it an agent may use. The
  guard is wired into agent hosts, so every command reaching it came from an
  agent by construction and a person at a terminal never meets this rule. The
  reason routes to `magus diff --impact`, which names every changed file
  carrying no receipt, and says to hand that list back rather than stamp it.
- **In-place stream edits**: `sed -i`, `sed --in-place`. The flag is not
  portable and the two spellings destroy each other's work: GNU reads
  `sed -i 's/x/y/' f` as an edit, BSD and macOS read that same script as the
  backup suffix, and the portable-looking `sed -i '' ...` makes GNU edit
  nothing. The command that worked where it was written mangles the file on the
  next machine, by writing, so the damage lands before anyone reads a diff.
  Every host driving this guard has a structured editor tool that applies an
  exact replacement and reports what changed. Reading with sed is untouched.
  A **scripted substitute-and-write** is the same edit by another route and
  denies with it: `perl -i` and `ruby -i` outright, and a `python` or `node`
  one-liner whose substitution (`re.sub`, `.replace(`) is followed on the line
  by a `.write(`. Deliberately narrow - an interpreter that only WRITES a file
  is ordinary authoring and passes - so a one-liner that writes before it
  substitutes slips through, and the rule is a habit rail rather than a fence.
- **Running magus from a copy of the workspace** in a temp or scratchpad
  directory (`cd /tmp/... && magus ...`, including via a variable assigned
  earlier on the same line). The verdict would describe a tree nobody ships:
  generated files land in the copy, the cache splits, and duplicated spell
  sources trip MGS1002. To work on a different workspace, pass `--root <path>`.
  A `cd` into a SIBLING CHECKOUT of this repository - a linked worktree, or the
  main checkout reached from inside one - denies on the same ground, recognized
  by reading the shared git directory rather than by the path's name: that
  tree's `./magus` was linked from ITS sources and its cache is keyed to ITS
  tree, so the verdict describes neither checkout. A cd into a genuinely
  different repository is not denied; that one only draws the `--root` advisory.

## What magus explains

An advise verdict carries context your host can inject while the call proceeds,
which only Claude Code does in full. Cursor delivers nothing on a command advise
and OpenCode logs it for the person. Codex differs in kind rather than degree: its
PreToolUse REJECTS `additionalContext` and fails open on it, so sending one there
disarmed the guard for that call, and magus now sends it nothing.

- `git commit` and `git add <paths>`: classify the dirty tree first. Deliberate
  staging is the replacement the rule above points at, so it is never denied.
- A path-scoped `git checkout -- <paths>` or `git restore`: regenerated output
  is a declared target output, and reverting it because you did not hand-edit it
  is what makes CI fail on drift.
- A repo-wide text search (`grep -r`, `rg`, `find -name`): the graph answers
  structural questions from declared sources - `magus refs` for a code symbol,
  `magus query` for a domain entity.
- A dependency re-resolution (`go get`, `pnpm add`, `cargo update`, `uv lock`,
  `pip-compile`): the `relock` charm is what grants that write inside magus, and
  it is deliberately not part of `rw` - `rw` covers output reproducible from a
  clean checkout, `relock` covers state that depends on what a registry serves
  today. Applying a lockfile (`npm ci`, `pnpm install --frozen-lockfile`)
  re-resolves nothing and passes. `go mod tidy` is the one that denies rather
  than advises, because a spell op renders it, and its deny reason carries the
  same `relock` route - routing into magus without naming the charm would send
  you to a target that refuses the write.
- A tree-identity read (`git rev-parse HEAD`, `git describe`, `git stash
  create`): `magus vcs checkpoint` prints the revision plus a digest of the
  uncommitted patch, which identifies a dirty tree where the revision alone
  cannot, and records it on the activity trail. The layout questions
  `git rev-parse` also answers (`--show-toplevel`, `--git-dir`, `--abbrev-ref`)
  pass, because a checkpoint does not replace them.
- `cd <dir> && magus ...` within the workspace: magus is CWD-relative and the
  project is always an explicit argument, so the `cd` is how the right command
  lands on the wrong project. A `cd` into a temp or scratchpad copy is denied
  instead, because that one changes what the answer means rather than only where
  it runs.
- `time magus ...`, `timeout 5m magus ...`, and `magus ... && echo done`: magus
  already reports each target's duration and verdict, already takes `--timeout`,
  and already reports success through its exit status.
- A chained `magus run` - a second `run` or `affected` after any of `;`, `&&` or
  `||`: targets compose through `ctx.needs`, so running the LAST one usually
  pulls the rest in, and each extra invocation reloads the workspace. Only the
  dependency graph knows whether the two are genuinely independent, which is why
  this advises rather than denies.

Everything else about the command itself passes. Two rules then read state
outside the command line, and speak only into the silence the rules above leave:

- The CI gate, when this workspace's run log shows it has already run several
  times in the last two hours at real cost: it runs everything the diff reaches
  by construction, and it is the target worth saving for the end. The rule reads
  the command, so running a narrower target draws silence rather than an
  advisory arguing with what it just asked for.
- A binary older than the guard rules in the tree: appended to every verdict,
  including a deny, because a stale binary's verdicts are all suspect rather
  than only the ones that matched.
- A graph read (`magus refs`, `query`, `explain`, `path`) against a symbol index
  older than the sources it describes. The command's own output says the same
  thing under the answer, which is the half that works on every host with
  nothing wired; this one arrives a call earlier.

## Advisories are said once

The advisories that carry a standing fact rather than a correction to the
command in front of you are held to one firing per session: the stale-binary
notice, the graph-beats-grep hint, the classify-before-staging reminder, the
index-staleness advisory, the enroll-a-lease notice an unleased write draws, and
the repository-scoped path rules above. The second
identical paragraph teaches nothing, and this page's standard says why that
matters - a check that is red by default is a check people learn to ignore,
taking the real failures with it.

Denials are exempt, and so is every reason a denial carries. A refusal explains
itself every time it refuses; it is the one verdict the caller cannot see past.
The advisories that correct the command itself - a `cd` before magus, a `time`
wrapper, a chained run - are exempt too, because a second firing reports a
second mistake.

A session is identified by the `session_id` your host reports, on the flag or in
the envelope. A host that reports none is not silenced forever: those notices
expire on a two-hour clock instead, so the next session is told again. The state
is one empty marker file per session and kind under the cache directory, swept
after a week.

## The file surface

`printf '%s' '<file>' | magus session hook --path` judges a file path rather
than a command. `--path` is a switch and takes no value: the path arrives on
stdin exactly as a command does. Three of its rules are definitive rather
than heuristic, because each reads DECLARATIONS: the
generated-output rule classifies the path against every target's declared
outputs, the notes rule against the declared notes store, and the lease rule
against what concurrent leases declared they own (see
[leases](leases.md)). The first advises, because a hand-edited generated
file is wasteful rather than destructive; the other two deny, on the provenance
trigger and on a collision no later rule can outrank.

The lease rule denies only where two DECLARED boundaries collide - an enrolled
lease writing onto another live lease's owned paths, onto its own forbidden
paths, or writing at all before it has registered the base it landed on. Four
cases it cannot decide that way advise instead:

- The ledger exists but will not parse. It says no boundary was checked rather
  than blocking on a file it cannot read, because a lease whose boundary
  silently stopped being checked looks exactly like one nobody declared.
- A write onto another live lease's owned paths by a writer magus cannot
  attribute to a live lease - naming none, naming an id it cannot parse, or
  naming a valid id with no live row. That is the same collision the enrolled
  case denies, and it advises because magus cannot tell "not in the fleet" from
  "in it and not saying so", and blocking a person in their own checkout is the
  worse of the two ways to be wrong. It also records the write against the
  owner, so the lease whose file just moved can find out by asking the ledger.
- An id that is not a valid lease id (at most 128 characters of `A-Za-z0-9-_./:`).
  The call is graded as if it named no lease and told so, rather than rejected:
  an id magus cannot parse is one it cannot look up either, and erroring would
  block a tool call over metadata.
- A lease whose registered base is not the checkpoint it was handed. An
  orchestrator may have rebased the plan deliberately, which magus cannot tell
  from a worker that wandered; what it can do is keep the divergence from
  staying silent until the merge finds it.

The rest are heuristics on the path, and each only fills a silence the
definitive rules leave: a cross-host instruction file (`AGENTS.md`, `CLAUDE.md`)
is where a workspace decision goes to be invisible to the next checkout, an
installed skill is generated and the next `--force` install erases the edit, and
a new source directory is a structural choice worth making deliberately rather
than by where a file happened to land.

Two more fire only inside magus's own checkout, identified the way the
stale-binary notice identifies it, and are inert in every other workspace: a
write to a shipped skill body or the MCP tool registry routes through the
authoring method those files are maintained by, and a write to a generator input
(a `.proto`, a Buzz host module descriptor) says to regenerate in the same
commit. Both name paths and a target that belong to this repository, which a
shipped verdict may not otherwise do; the gate is what makes them legitimate,
because outside this repository neither can fire at all.

One rule reads the environment rather than the path. A process carrying spawn
ancestry that writes while naming no lease, in a workspace whose ledger holds no
live row, is told how to enroll. The ancestry is a claim any local process can
set, so it may teach and may not judge: it can only ever turn silence into an
advisory, never deny, and never change what another rule decided.

All of them say nothing on any uncertainty. A rule fired on a guess trains the
reader to ignore it, and a deny fired on a guess blocks real work.

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
printf '%s' 'git stash' | magus session hook -o json
printf '%s' 'go test ./...' | magus session hook -o name
printf '%s' 'MAGUS.md' | magus session hook --path -o name
```

A deny exits 2 with the verdict on stdout; a pass and an advise exit 0. An
empty event passes, but one the hook cannot read fails closed as a deny:
nothing was judged, so the call is blocked rather than cleared.

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

Every `magus session hook` invocation with a readable command or path appends one
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
the name in with `magus session hook --agent-name`; a wrapper that does not leaves the
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

One payload shape is recorded and never judged. A hook event carrying a `prompt`
rather than a command or a file path is a lease handoff: it appends an
`agent_spawn` event and returns `pass` without evaluating a rule, because there
is no command and no path to judge, and a prompt that merely mentions a denied
command would otherwise block the lease that describes it. See
[Leases](leases.md).

## Measuring adoption

The point of the grep-to-query nudge is to move a number: how often agents reach
for the knowledge graph versus a raw text search. `magus agent adoption` reports
it from a corpus of shell commands - the graph-to-grep ratio, the file reads a
targeted read would beat, and the top repo-wide greps whose pattern is a real
identifier, the ones that should have been `magus refs`.

magus analyzes commands; it never reads a host's session logs, so extraction is
yours. For Claude Code, whose sessions are JSONL under `~/.claude/projects/`:

```sh
cat ~/.claude/projects/*/*.jsonl \
  | jq -r '.message.content[]? | select(.type=="tool_use" and .name=="Bash")
           | (.input.command | split("\n")[0])' \
  | magus agent adoption
```

A 1:20 ratio means the graph is barely used. The levers that move it are an
easier query grammar to reach for (`kind=x`, `id=~re`) and the advisory that
translates a caught grep into the `magus refs` that would answer it.
