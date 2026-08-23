---
title: Delegation
description: The surface magus gives an agent that fans work out - the working-state checkpoint, the declared delegation ledger, the console Plan surface, the recorded spawn, and verifying a delegation against the diff since its checkpoint.
tags:
  [
    agents,
    delegation,
    checkpoint,
    ledger,
    plan,
    magus vcs checkpoint,
    magus_ledger,
    console,
    activity,
  ]
---

# Delegation

One agent hands work to several. magus neither runs that fan-out nor polices
it. It answers what the working state is, records what the orchestrating agent
says it intends, and shows a person the result - the same rule the rest of the
[agent surface](../agents.md) obeys: answering is the tool's job, deciding is
the model's.

How to split the work is not on this page. That is the
[`magus-delegate-multi-agent` skill](../../../reference/skills/magus-delegate-multi-agent.md):
partition by write set rather than by affected project, prove the delegations cannot
collide, bound the fan-out, match a model to each delegation. This page is the surface
that skill writes to and reads from.

| step                     | surface                                            |
| ------------------------ | -------------------------------------------------- |
| Record the working state | `magus vcs checkpoint`, `magus_vcs_checkpoint`     |
| Hand out the delegations | the host's own spawn - recorded, never judged      |
| Declare the plan         | `magus_ledger` (`op=put`)                          |
| Watch it                 | the console Plan surface, `GET /api/v1/ledger`     |
| Verify                   | the actual diff since each delegation's checkpoint |

No surface in that table enforces anything. The ledger is a declaration, the
checkpoint is a reading, and the Plan surface renders both; a worker that writes
outside its owned paths is caught by the last step, not by the third.

<!--diagram:delegation-loop-->

## Record the working state

A checkpoint is the identity of the tree right now: the head revision, the
branch carrying it, whether the tree is dirty, and a digest of the uncommitted
patch.

```sh
magus vcs checkpoint
# <revision> <branch> clean
# <revision> <branch> dirty <digest>

magus vcs checkpoint -o name
# <revision>              when the tree is clean
# <revision>+<digest>     when it is not

magus vcs checkpoint -o json
# the whole record: revision, branch, dirty, patch_digest, vcs
```

It resolves and records; it never mints. No tag, no stash, no ref, no file,
nothing changed anywhere - so taking one per delegation costs the tree
nothing, and one nobody keeps costs nothing either.

The digest is the half a revision cannot supply. Every worker on a branch shares
the revision, so a dirty tree's revision does not say WHICH dirty tree was
handed out; comparing two digests does. That is why `-o name` renders a dirty
checkpoint as `<revision>+<digest>` and a clean one as the bare revision: the
clean form is a token anyone can check out, and the `+` marks the other as a
revision plus uncommitted work that nobody can.

`-o name` is the single citable token, sized for the one cell a ledger row gives
it. Feed the revision half to anything that takes a revision, such as
`magus graph diff --rev <revision>`.

The command takes no arguments - it reports the whole workspace's working state.
A path argument is refused rather than ignored, because
`magus vcs checkpoint <path>` would read as a path-scoped digest, which is a
different and much narrower fact. Agents connected over
[MCP](../mcp.md) call `magus_vcs_checkpoint`, which takes no parameters and
returns the same record. Full flags: [`magus vcs`](../../../reference/manpage/magus-vcs.md).

## Declare the plan in the ledger

`magus_ledger` records the delegation plan an orchestrating agent declared, so a
person can see it. One row per delegation, in the skill's vocabulary.

| op      | does                                                                       |
| ------- | -------------------------------------------------------------------------- |
| `list`  | every row in the order they were recorded, plus the overlaps (the default) |
| `put`   | create or replace one row by `id`, merging the fields you send             |
| `clear` | drop every row and start a fresh plan                                      |

A row carries `id` and optionally `parent` (the delegation that delegated this one),
`goal` with its observable acceptance criteria, `checkpoint` (as
`magus vcs checkpoint -o name` prints it), `owned_paths` and `forbidden_paths`,
`depends_on`, `tier`, `validation`, `state`, and `read_only`. The store adds
`created`, `updated`, and `releases`, which are output-only: a timestamp a client
sent would be a fact about that client's clock.

Three properties are worth stating plainly.

**Owned and forbidden paths are a declaration, not a boundary.** Nothing here
blocks a write, gates a run, or derives a verdict. They are the text an
orchestrator put in a worker's prompt, written down where a human can read it.
Ownership is checked by comparing this ledger against the actual diff since each
delegation's checkpoint, which is the last step below.

**Every row ends in `pass`, `fail`, or `no_return`.** `no_return` is not a
failure. A delegation that failed came back and said so; a delegation that died, stalled, or
was cancelled said nothing, and it is the only state on this surface that no
other system will report. Silence is not a pass.

**One plan per workspace, with no history.** `clear` starts a fresh one and keeps
nothing. A read-only delegation that gathers evidence and writes nothing carries an
abbreviated row: `read_only` set, and empty owned and forbidden paths that then
read as deliberate rather than forgotten.

### Three answers the ledger gives back

None of them is enforcement. Each is something an orchestrator would otherwise
derive by hand from a table it wrote itself, and each leaves the decision where
it was.

**Overlaps.** A `list` reports every pair of delegations whose `owned_paths` intersect
as `unit_a`/`unit_b` and `paths_a`/`paths_b` - each side's own declarations, kept
apart, because they are rarely the same string and which delegation claimed which is
the part a reader acts on. Derived on the read and stored nowhere, so
it cannot go out of date with the rows. A path is compared by containment - a delegation
owning `internal/ledger` overlaps one owning `internal/ledger/store.go` - and a
glob is judged by the directories it names, which over-reports rather than misses
a pair: `console/src/**/*.ts` and `console/src/**/*.css` share no file and are
reported anyway. A delegation in a terminal state is in no pair, because a finished or
released delegation is not competing for anything.

**Staleness.** Every put re-stamps `updated`. A row nobody touches goes quiet, and
a reader watching that gap may judge the delegation possibly dead - the console draws
the age on live rows and marks one that has not moved in ten minutes. The judgment
is the reader's: no row transitions itself, and `no_return` is only ever a state an
agent wrote.

**Releases.** Shrinking `owned_paths` is how a delegation announces it has finished
editing a path, and the store records each dropped path with the digest that path
carried at that moment: the file's sha256, or one of three words when it cannot
be one - `absent` when nothing is there, `dir` for a directory, which has no
single content hash, and `unreadable` for a path that is there and could not be
hashed (a permission denied, something that is not a regular file, a file over
the store's size cap). `absent` and `unreadable` are deliberately not the same
answer: "the releaser deleted it" and "something is there nobody could read"
send you to different places. Computed here so no
worker has to hash anything, and so the digest describes the tree the releaser
actually left rather than the one it believed it left. Hand it to the delegation taking
the path over; a digest that no longer matches at verification time means that
delegation built on a tree the releaser never saw.

The rows live in one JSON file under the cache directory
(`<cache-dir>/ledger/units.json`). `magus_ledger` is its write door and the
daemon's read-only `GET /api/v1/ledger` route is its read door; there is no CLI
verb, because the ledger has a single author by definition of what it records -
the one agent doing the orchestrating. The store takes no cross-process lock, so
do not point two orchestrators at one workspace.

## Watch it: Dashboard's Delegation plan

The [console](../../../reference/console.md)'s Dashboard includes a Delegation
plan mode that draws a plan as the DAG it is, from either of the two places a
plan comes from. Both share the stage, the state colors, and the accessible node
list beside the drawing, so a reader does not learn the picture twice.

- **The declared plan** is the ledger: one node per delegation, indented by `parent`,
  joined to the live activity feeds so a row shows what its worker is doing now.
  A delegation in a reported overlap is marked on both rows, in the word "overlap" and
  in the warning color; a live row carries how long since it was last touched, and
  says "stale" once that passes ten minutes. The released paths and their digests
  read in the detail beside the row.
- **The run plan** is the target DAG the engine resolves for plain human work,
  served by `GET /api/v1/plan`. Nobody declared it, so nothing about it can go
  stale the way a hand-kept table can, and it follows the live run: with no
  `?target` the daemon picks the anchor itself and the overview line says how it
  picked.

Which one opens is decided by the data rather than by a preference. A ledger
with rows in it means an orchestration is in flight, which is the more specific
answer; anything else hands the surface to the run plan, which is what a person
doing plain work came for. `no_return` gets its own color and belongs to the
declared plan alone - the run plan never invents one, because an engine that
resolved a DAG knows what happened to every node in it.

Both routes are read-only GETs on the loopback daemon, behind the same bearer
token as the rest of the console. Start it with `magus server start`; see
[the daemon](../daemon.md).

## The spawn is recorded, never judged

Wire your host's sub-agent tool to the same `magus session hook` call as the rest of
[the guard](guard.md). A payload carrying a `prompt` rather than a command or a
file path is a delegation handoff: magus records it as an `agent_spawn` event on
the local Activity Trail and returns `pass` without evaluating a single rule.

That exemption is the point, not an oversight. There is no command and no path
to judge, only a context transfer to note - and a prompt that merely MENTIONS a
denied command would otherwise block the delegation that describes it. The
handed context is routinely kilobytes, so it lands as a content-addressed blob
and only its reference rides the event.

magus does not switch on your host's tool name anywhere: a payload carrying a
prompt IS a spawn, and the callee label the host supplies becomes the event's
action so a page of delegations groups by what was spawned.

Joining an event to a ledger row is cooperative. Nothing in a host event names a
magus delegation and magus will not infer one from prose, so the delegation is stamped only
when the handed context's FIRST non-blank line reads:

```text
delegation: <id>
```

The older spelling `unit: <id>` is still read, so a prompt template written
before the rename keeps joining; write `delegation:` in anything new.

Use the same id you passed to `magus_ledger`. An orchestrator that wants the
join writes the marker; one that does not gets an event with no delegation, which is a
missing join rather than a wrong one. A marker line quoted deeper in a prompt
stamps nothing, on purpose.

## Verify against the diff

A worker's report of what it changed is a claim. The checkpoint is what turns it
into something you can check.

1. Diff the actual tree against the delegation's checkpoint, and compare THAT against
   the row's owned and forbidden paths. `magus graph diff --rev <revision>`
   gives the domain-level answer; `git diff <revision> | magus diff -` annotates
   each changed file with its reach, public-surface exposure, and referents. See
   [`magus diff`](../../../reference/manpage/magus-diff.md) - it refuses a git
   ref given positionally, so the pipe is the sanctioned spelling.
2. Check the dirty half of the token. A checkpoint whose digest differs from the
   tree you are diffing means the worker saw a different uncommitted tree, and
   the comparison you are about to make is not the one you think.
3. Take acceptance evidence as an [output reference](../../../concepts/cache/output-refs.md)
   the root reopens, never a worker's prose. A worker that ran a filtered subset
   and one that quietly restated its criteria both report success, and a
   transcript cannot tell you which happened.
4. Regenerate declared outputs once, centrally, after the source work converges,
   then run the release gate yourself.

The same object serves review time. If you recorded a checkpoint when you
stopped reading, the delta since then is the incremental-review flow on the
[agents hub](../agents.md#incremental-review) - handing work out and picking
review back up read the same identity.

## What magus never does here

- Block a write outside a delegation's owned paths, or any write at all on account of
  a ledger row.
- Derive a verdict, a state, or a completion from anything in the ledger. Every
  state in it was written by the agent that declared the plan.
- Judge a delegation prompt, or let one change a guard verdict.
- Mint anything for a checkpoint - no tag, no stash, no ref, no file.
- Inject any of this into an agent's context. Every surface here is pull-based,
  and the [knowledge graph](../../../concepts/knowledge.md) the partition is
  argued from is read the same way.
