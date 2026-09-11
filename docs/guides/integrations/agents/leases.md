---
title: Leases
description: The surface magus gives an agent that fans work out - the working-state checkpoint, the declared lease ledger, the console Plan surface, the recorded spawn, and verifying a lease against the diff since its checkpoint.
tags:
  [
    agents,
    leases,
    checkpoint,
    ledger,
    plan,
    magus vcs checkpoint,
    magus_ledger,
    magus ledger,
    brief,
    console,
    activity,
  ]
aliases: [guides/integrations/agents/delegation]
---

# Leases

One agent hands work to several. magus neither runs that fan-out nor polices
it. It answers what the working state is, records what the orchestrating agent
says it intends, and shows a person the result - the same rule the rest of the
[agent surface](../agents.md) obeys: answering is the tool's job, deciding is
the model's.

How to split the work is not on this page. That is the
[`magus-multi-agent` skill](../../../reference/skills/magus-multi-agent.md):
partition by write set rather than by affected project, prove the leases cannot
collide, bound the fan-out, match a model to each lease. This page is the surface
that skill writes to and reads from.

| step                     | surface                                        |
| ------------------------ | ---------------------------------------------- |
| Record the working state | `magus vcs checkpoint`, `magus_vcs_checkpoint` |
| Hand out the leases      | the host's own spawn - recorded, never judged  |
| Declare the plan         | `magus_ledger` (`op=put`)                      |
| Brief each worker        | `magus ledger brief <lease-id>`                |
| Watch it                 | `magus ledger`, the console Plan surface       |
| Verify                   | the actual diff since each lease's checkpoint  |

Only one thing in that table enforces, and it is not the ledger. The ledger is a
declaration, the checkpoint is a reading, and the Plan surface renders both. The
[guard](guard.md) is what reads the declaration back, on every file write and
every command: see [what the guard enforces under a lease](#what-the-guard-enforces-under-a-lease). It grades only a worker that named
its lease, so the last step below - the actual diff against the checkpoint - is
still what catches a write nobody could attribute.

<!--diagram:lease-loop-->

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
nothing changed anywhere - so taking one per lease costs the tree
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

`magus_ledger` records the lease plan an orchestrating agent declared, so a
person can see it. One row per lease, in the skill's vocabulary.

| op         | does                                                                           |
| ---------- | ------------------------------------------------------------------------------ |
| `list`     | every row in the order they were recorded, plus the overlaps (the default)     |
| `put`      | create or replace one row by `id`, merging the fields you send                 |
| `register` | record the base a worker actually landed on, and read the verdict comparing it |
| `clear`    | drop every row and start a fresh plan                                          |

A row carries `id` and optionally `parent` (the lease that handed out this one),
`goal` with its observable acceptance criteria, `checkpoint` (as
`magus vcs checkpoint -o name` prints it), `owned_paths` and `forbidden_paths`,
`focus`, `depends_on`, `tier`, `validation`, `state`, and `read_only`. The store adds
`schema_version` (the row shape, currently 1, required on anything you send and
rejected by name when it is a version magus does not know), `registered_by` (the
session and host that created the row), `created`, `updated`, `releases`, and
`unattributed` (paths this lease owns that somebody outside it wrote, noticed by
the guard), all output-only: a timestamp a client sent would be a fact about that
client's clock. `register` adds three more
the same way - `reported_base`, the checkpoint token the worker found in its OWN
tree; `base_verdict`, the store's comparison of that against the row's
`checkpoint`; and `registered`, when that comparison was recorded.

Five properties are worth stating plainly.

**One author per row, enforced by the store.** A session acting under a lease may
register the base it landed on, SHRINK its own `owned_paths` (which is how it
releases a path), end its own row in `fail` or `no_return`, and declare a child of
itself inside its own paths. Every other write is refused, by name and with the
remedy: widening a lane, changing the plan's shape, clearing the book, and
accepting a row are the orchestrator's. The rule lives in the store rather than in
a guard pattern because the CLI, the MCP tool and `magus\ledger` all reach the same
file and only one of them is a command a pattern can read. A session that is bound
to no lease - the orchestrator, or a person at a terminal - writes anything.

**Binding is one-way.** `magus session lease <id>` on a checkout already bound to a
different lease is refused. Rebinding is how a worker would be graded against
another lease's paths, and it costs nothing to a worker that runs its bootstrap
twice: binding the lease it already holds is allowed and does nothing.

**A declared boundary is enforced elsewhere.** Beyond the row ownership above, this
store gates nothing: it records the text an orchestrator put in a worker's prompt,
where a human can read it. The [agent guard](guard.md) is the one reader that turns
it into a verdict;
what it refuses is listed under [what the guard enforces under a lease](#what-the-guard-enforces-under-a-lease) below. Every uncertainty there
fails open with at most an advisory - no ledger, an unreadable one, a writer that
named no lease - because this is a seatbelt for a harness that opted in and not a
sandbox. Which is why the diff since each lease's checkpoint, the last step
below, is still where ownership is finally checked.

**Every row ends in `pass`, `fail`, or `no_return`.** `no_return` is not a
failure. A lease that failed came back and said so; a lease that died, stalled, or
was cancelled said nothing, and it is the only state on this surface that no
other system will report. Silence is not a pass.

**One plan per workspace.** `clear` starts a fresh one, copying the rows it drops
to a timestamped `leases-<when>.json` beside the ledger first. Nothing reads those
back: they exist so that a plan somebody else wiped is still legible to a person.
A read-only lease that gathers evidence and writes nothing carries an abbreviated
row: `read_only` set, and empty owned and forbidden paths that then read as
deliberate rather than forgotten.

### Three answers the ledger gives back

None of them is enforcement. Each is something an orchestrator would otherwise
derive by hand from a table it wrote itself, and each leaves the decision where
it was.

**Overlaps.** A `list` reports every pair of leases whose `owned_paths` intersect
as `lease_a`/`lease_b` and `paths_a`/`paths_b` - each side's own declarations, kept
apart, because they are rarely the same string and which lease claimed which is
the part a reader acts on. Derived on the read and stored nowhere, so
it cannot go out of date with the rows. A path is compared by containment - a lease
owning `internal/ledger` overlaps one owning `internal/ledger/store.go` - and a
glob is judged by the directories it names, which over-reports rather than misses
a pair: `console/src/**/*.ts` and `console/src/**/*.css` share no file and are
reported anyway. A lease in a terminal state is in no pair, because a finished or
released lease is not competing for anything.

**Staleness.** Every put re-stamps `updated`. A row nobody touches goes quiet, and
a reader watching that gap may judge the lease possibly dead - the console draws
the age on live rows and marks one that has not moved in ten minutes. The judgment
is the reader's: no row transitions itself, and `no_return` is only ever a state an
agent wrote.

**Releases.** Shrinking `owned_paths` is how a lease announces it has finished
editing a path, and the store records each dropped path with the digest that path
carried at that moment: the file's sha256, or one of three words when it cannot
be one - `absent` when nothing is there, `dir` for a directory, which has no
single content hash, and `unreadable` for a path that is there and could not be
hashed (a permission denied, something that is not a regular file, a file over
the store's size cap). `absent` and `unreadable` are deliberately not the same
answer: "the releaser deleted it" and "something is there nobody could read"
send you to different places. Computed here so no
worker has to hash anything, and so the digest describes the tree the releaser
actually left rather than the one it believed it left. Hand it to the lease taking
the path over; a digest that no longer matches at verification time means that
lease built on a tree the releaser never saw.

The rows live in one JSON file in the per-REPOSITORY state directory
(`<XDG state>/magus/ledger/<repo>/leases.json`), keyed the way
[memory](../../../reference/manpage/magus-memory.md) and session history are
keyed: every worktree and every clone of one repository reads one book. That is
what lets an orchestrator declare a plan in its own checkout and a worker read
its lease from another. A ledger an older magus left at `<cache-dir>/ledger` is
carried forward the first time the new one opens it.

Two channels write it and one reads it: `magus_ledger` is the agent's,
`magus ledger` is the person's, and the daemon's `GET /api/v1/ledger` route is
read-only. Both write channels reach one store and one set of rules, which is where
the single-author property lives now. It used to live in the closed door: writing
stayed off the CLI on the ground that the plan has one author, which left a person
unable to declare a row without an agent to do it for them. One author per ROW is
the true version of that rule, and the store enforces it.

```sh
magus ledger                      # the rows as a tree, parents above the leases they handed out
magus ledger -o json              # the same records, overlaps included
magus ledger brief <lease>        # one lease's worker brief
magus ledger register <lease> \
  --goal 'move the store' \
  --owned internal/ledger \
  --validation 'magus run test internal/ledger'
magus ledger register --stdin < row.json   # the same row as a record
magus ledger register --schema             # what that record must satisfy
```

## Wiring the lease into a worker

A declared boundary grades nothing until the writing process says which lease it
is, and it says that through its ENVIRONMENT. So the orchestrator that spawns a
worker exports the id into that worker's environment, and the hook process the
worker's host launches inherits it from there:

```sh
export BAGGAGE=magus.lease=<its id>
```

That is the W3C Baggage channel, carried in the environment under the
OpenTelemetry convention, and `magus.lease` is the one member a verdict reads.
Export `TRACEPARENT` too when your host has one, and add
`magus.spawner=<your label>` to the baggage: magus records the trace, the parent
span and the label as CLAIMS for `magus session ls` to show, and keys no verdict
on them. The [`magus-multi-agent` skill](../../../reference/skills/magus-multi-agent.md)
requires this of every worker prompt it writes, in the same spelling.

Exporting it is the ORCHESTRATOR's job today. The shipped
[guard templates](guard-templates.md) pass `--agent-name` and `--session`, which
are attribution, and nothing that names a lease - so a worker whose orchestrator
never exported the variable is graded as an editor magus cannot attribute, which
is an advisory rather than a deny and leaves every rule above it inert. A wrapper
that builds its own argv can pass `magus session hook --lease <id>` instead; an
explicit flag wins over the environment.

## What the guard enforces under a lease

Once a worker names a live lease, the [guard](guard.md) reads that row on every
file write and every command. It denies:

| the guard refuses                                              | the row field that decided it |
| -------------------------------------------------------------- | ----------------------------- |
| any write, before the worker registers the base it landed on   | `registered`                  |
| any write, by a lease that gathers evidence and writes nothing | `read_only`                   |
| a write covered by this lease's own forbidden list             | `forbidden_paths`             |
| a write covered by another live lease's owned list             | `owned_paths` (that lease's)  |
| a write outside every entry in this lease's own owned list     | `owned_paths`                 |
| a command running the `ci` gate                                | `validation`                  |
| a READ of a path outside the projects this lease may read      | `focus`, else `owned_paths`   |

It advises on one more: your own path, written from a base that `base_verdict`
says is not the checkpoint you were handed.

The read row is the write lane read the other way. `owned_paths` stands in when
`focus` is empty, because a worker leased to edit a project was pointed at that
project, and the lane it opens is those projects plus what they declare
`depends_on` (see [the guard's focus rule](guard.md#focus-the-read-lane)). Set
`focus` when a worker must READ something it must not WRITE: widening
`owned_paths` to open a read is how two workers end up owning one file. Without a
lease bound to the checkout the same rule only advises, which is the opt-in: a
hard read boundary needs somebody to have declared one.

The last row is the one that surprises people. The gate runs ONCE per branch, in
the orchestrator's tree, after every unit lands; a worker's `validation` is the
narrow target it was assigned, so a worker that reaches for the whole pipeline is
refused and handed its own check instead. A row whose `validation` names `ci`
owns the gate and is not refused.

Two absences are boundaries nobody declared rather than boundaries of size zero,
and both scope nothing: an empty `owned_paths` on a row that is not `read_only`,
and an empty `validation`.

None of that table is visible from the verdict a worker sees, which is exactly
what makes a wrong binding dangerous: a checkout bound to an unknown id, a
terminal row, or a live row that never registered its base all render as an
ordinary advisory, indistinguishable from a session these rules are actually
enforcing on. `magus doctor`'s **lease-enforcing** check is the other end of
that gap - it reads the same row the guard would and says, in one line,
whether this checkout's bound lease is live, registered, and therefore
actually judged, or names which of the three it is not.

## What the sandbox enforces under a lease

The guard above is a seatbelt for harnesses that opt in: it explains a boundary
and, for a worker, denies the tool call that crosses it. The
[sandbox](../../../concepts/sandbox.md) is the boundary itself, and it reads the
same ledger row rather than a second declaration - a boundary written twice is a
boundary that disagrees with itself.

When `sandbox.enabled` is true and the acting lease resolves to a live row with a
`parent` and non-empty `owned_paths`, every target run and every `magus buzz`
script in that checkout gets a filesystem WRITE grant of exactly:

- the `owned_paths`, resolved as globs against the workspace root (a glob that
  matches nothing grants nothing). A LITERAL path that does not exist yet grants
  the nearest directory above it that does, because a lease routinely owns a
  file it was spawned to create and the guard already admits that write; only a
  glob keeps the existing-files-only rule, since a typo in a glob is the case
  that rule protects against;
- the workspace cache directory and `$TMPDIR`, which a target needs to produce
  output at all.

Reads are untouched: the row declares a write boundary, and a worker has to read
the tree it is changing. A refusal is recorded on the trail as a
`sandbox_denial` carrying the lease id, so a reader can say whose boundary was
hit rather than only that something was blocked.

Nothing narrows for a ROOT lease (a row with no parent is the orchestrator, and
it owns the checkout), for a lease id that names no live row, for a writable row
with no owned paths, or when the sandbox is off. A `read_only` row narrows to
the cache directory and `$TMPDIR` alone, which is the sandbox's reading of the
guard refusing every write under such a lease.

Both tiers resolve the acting lease the same way, in this order: an explicit
`--lease`, the `BAGGAGE` a worker inherited, then the marker `magus session
lease <id>` bound to the checkout. A host runs its hooks from wherever it likes,
so the guard locates that checkout from the `cwd` its hook envelope reports and
falls back to the hook process's own directory only when the envelope carries
none.

A `forbidden_path` inside an owned one is refused, and it costs the directory
holding it as well: both this policy and landlock are allowlists with no deny
rule, so the enclosing grant is replaced by grants on its children, and a new
file created beside the forbidden entry is refused with it.

## Briefing a worker

The context a delegated agent receives should come from the row, not from the
orchestrating model's recollection of it. `magus ledger brief <lease-id>` renders
that context and nothing else: the lease id and the one line that binds it to a
checkout, the goal and acceptance criteria verbatim from the row, the owned and
forbidden paths, the knowledge graph's blast radius for each owned path it can
resolve, the single validation target that lease is allowed to run, and the
leases it depends on. A section with nothing in it is dropped, which is the
mechanism rather than a nicety: `ci` cannot leak into a worker's brief because
nothing renders it. When the graph is cold the evidence lines are replaced by one
line saying so, so a missing blast radius never reads as "nothing depends on
this".

Below that sits `bootstrap`: the commands the worker runs before it edits
anything, each with the reason it exists - confirm the worktree is clean, bind the
lease, record and register the base. Commands, because the guard states every RULE
at the moment a command meets it; a rules block in a brief is a paragraph read once
and a denial is a sentence read when it matters. The brief carried one until
2026-09-11, and measuring it settled the question: a worker whose brief said in so
many words never to stash tried it twice anyway, and what stopped it was the deny.
The command renders context and never a verdict, the same shape
`magus diff --prompt` has: magus assembles what it holds and you hand it to the
worker. `-o json` emits the same brief as a record, `bootstrap` included as
`{run, why}` pairs, for an orchestrator that composes the prompt itself.

## Grading what comes back

`magus ledger accept <lease-id> --stdin < report.json` grades a finished worker's
report against its row, and it grades EVIDENCE rather than what the report claims:

- every changed path inside the declared `owned_paths`, and a change set that is
  not empty on a row that is not `read_only`;
- an `output_ref` that still resolves, whose stored run is a run of THIS row's
  `validation` (compared as project, target and spell filter, so spelling does not
  decide it) and which the store recorded as passing.

There is no `passed` field in the report. A worker's verdict on its own run is the
assertion the ref exists to replace: on 2026-09-11 a report with no changed paths,
an output ref from an unrelated codegen run, `passed: true`, and two invented
fields was accepted and recorded `pass`. Unknown fields are now refused, the report
carries a required `schema_version`, and a session bound to a lease cannot grade
any row, its own included.

Two failing statuses, because they send a caller somewhere different: 2 when the
report could not be decoded (a bad shape, an unknown version), 1 when it was read
and rejected, with one line per rule that failed. `--stdin` is required: without
it the command says so rather than waiting on a terminal.
`magus ledger accept --schema` prints the schema a report must satisfy.

## Watch it: Dashboard's Lease plan

The [console](../../../reference/console.md)'s Dashboard includes a Lease plan
mode that draws a plan as the DAG it is, from either of the two places a plan
comes from. Both share the stage, the state colors, and the accessible node list
beside the drawing, so a reader does not learn the picture twice.

- **The declared plan** is the ledger: one node per lease, indented by `parent`,
  joined to the live activity feeds so a row shows what its worker is doing now.
  A lease in a reported overlap is marked on both rows, in the word "overlap" and
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
file path is a lease handoff: magus records it as an `agent_spawn` event on
the local Activity Trail and returns `pass` without evaluating a single rule.

That exemption is the point, not an oversight. There is no command and no path
to judge, only a context transfer to note - and a prompt that merely MENTIONS a
denied command would otherwise block the lease that describes it. The
handed context is routinely kilobytes, so it lands as a content-addressed blob
and only its reference rides the event.

magus does not switch on your host's tool name anywhere: a payload carrying a
prompt IS a spawn, and the callee label the host supplies becomes the event's
action so a page of leases groups by what was spawned.

Joining an event to a ledger row is cooperative. Nothing in a host event names a
magus lease and magus will not infer one from prose, so the lease is stamped only
when the handed context's FIRST non-blank line reads:

```text
lease: <id>
```

Use the same id you passed to `magus_ledger`. An orchestrator that wants the
join writes the marker; one that does not gets an event with no lease, which is a
missing join rather than a wrong one. A marker line quoted deeper in a prompt
stamps nothing, on purpose.

## Verify against the diff

A worker's report of what it changed is a claim. The checkpoint is what turns it
into something you can check.

1. Diff the actual tree against the lease's checkpoint, and compare THAT against
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

- Block a writer it cannot attribute. Only a process that named a live lease is
  graded against a declared boundary; anyone else editing this workspace is
  advised at most, because a human in their own checkout names no lease either.
- Transition a row, or derive a state or a completion from one. Every state in
  the ledger was written by the agent that declared the plan.
- Judge a lease prompt, or let one change a guard verdict.
- Mint anything for a checkpoint - no tag, no stash, no ref, no file.
- Inject any of this into an agent's context. Every surface here is pull-based,
  and the [knowledge graph](../../../concepts/knowledge.md) the partition is
  argued from is read the same way.
