---
title: Jobs and leases
description: The surface magus gives an agent that fans work out - the working-state checkpoint, the declared job store, the lease a holder takes on a job, the console Jobs view, the recorded spawn, and verifying a result against the diff since the checkpoint.
tags:
  [
    agents,
    jobs,
    leases,
    checkpoint,
    magus vcs checkpoint,
    magus job,
    magus_job,
    console,
    activity,
  ]
aliases: [guides/integrations/agents/delegation]
---

# Jobs and leases

One agent hands work to several. magus neither runs that fan-out nor polices
it. It answers what the working state is, records what the orchestrating agent
says it intends, and shows a person the result: the same rule the rest of the
[agent surface](../agents.md) obeys, where answering is the tool's job and
deciding is the model's.

Two nouns, and the difference decides how everything below reads. A **job** is
the unit of delegated work: one row, with a goal, a checkpoint, the paths it may
write, and the one check it runs. A **lease** is the grant a holder takes on a
job: the write and read lanes that job declared, enforced in the checkout that
took it. A job is the thing; a lease is permission over it.

How to split the work is not on this page. That is the
[`magus-multi-agent` skill](../../../reference/skills/magus-multi-agent.md):
partition by write set rather than by affected project, prove the jobs cannot
collide, bound the fan-out, match a model to each job. This page is the surface
that skill writes to and reads from.

| step                      | surface                                         |
| ------------------------- | ----------------------------------------------- |
| Record the working state  | `magus vcs checkpoint`, `magus_vcs_checkpoint`  |
| Declare the work          | `magus job fork`, `magus_job`                   |
| Hand out the jobs         | the host's own spawn, recorded but never judged |
| Give the holder its terms | `magus describe job <job>`                      |
| Take the lease            | `magus job exec <job>`                          |
| Watch it                  | `magus ls jobs`, the console Jobs view          |
| Return and verify         | `magus job exit`, `magus job wait`              |

Only one thing in that table enforces, and it is not the store. The job row is a
declaration, the checkpoint is a reading, and the console renders both. The
[guard](guard.md) is what reads the declaration back, on every file write and
every command: see
[what the guard enforces under a lease](#what-the-guard-enforces-under-a-lease).
It grades only a holder that took its lease, so the last step below, the actual
diff against the checkpoint, is still what catches a write nobody could
attribute.

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
nothing changed anywhere, so taking one per job costs the tree nothing, and one
nobody keeps costs nothing either.

The digest is the half a revision cannot supply. Every worker on a branch shares
the revision, so a dirty tree's revision does not say WHICH dirty tree was
handed out; comparing two digests does. That is why `-o name` renders a dirty
checkpoint as `<revision>+<digest>` and a clean one as the bare revision: the
clean form is a token anyone can check out, and the `+` marks the other as a
revision plus uncommitted work that nobody can.

`-o name` is the single citable token, sized for the one cell a job row gives
it. Feed the revision half to anything that takes a revision, such as
`magus graph diff --rev <revision>`.

The command takes no arguments: it reports the whole workspace's working state.
A path argument is refused rather than ignored, because
`magus vcs checkpoint <path>` would read as a path-scoped digest, which is a
different and much narrower fact. Agents connected over
[MCP](../mcp.md) call `magus_vcs_checkpoint`, which takes no parameters and
returns the same record. Full flags: [`magus vcs`](../../../reference/manpage/magus-vcs.md).

## Declare the work

`magus job fork` records one job: what its holder is handed, where it may write,
and the one check it runs. It replaces any job with the same id.

```sh
magus job fork <job> \
  --goal 'move the store' \
  --write-paths internal/job \
  --check 'test internal/job'

magus job fork --stdin < job.json   # the same job as a record
magus job fork --schema             # what that record must satisfy
```

A row carries `id` and optionally `parent` (the job this one was forked from),
`goal` with its observable acceptance criteria, `checkpoint` (as
`magus vcs checkpoint -o name` prints it), `write_paths`, `deny_paths`,
`read_paths`, `depends_on`, `model`, `check`, `state`, and `read_only`. The
store adds `schema_version`, the actor that recorded the row, `created`,
`updated`, `releases`, and `unattributed` (paths this job owns that somebody
outside it wrote, noticed by the guard), all output-only: a timestamp a client
sent would be a fact about that client's clock.

There is no `--state` on `fork`. It declares a NEW job, and one nobody has taken
is `declared`; a holder moves its own job with `magus job exec` and
`magus job exit`. Every job ends in `pass`, `fail`, or `no_return`, and
`no_return` is not a failure: a job that failed came back and said so, while one
that died, stalled, or was cancelled said nothing. Silence is not a pass.

Five properties are worth stating plainly.

**One author per job, enforced by the store.** A session holding a lease may
record the base it landed on, SHRINK its own `write_paths` (which is how it
releases a path), end its own job, and fork a child of itself inside its own
paths. Every other write is refused, by name and with the remedy: widening a
lane, changing the plan's shape, and verifying a result are the forking
session's. The rule lives in the store rather than in a guard pattern because
the CLI, the `magus_job` MCP tool and `magus\job` all reach the same file and
only one of them is a command a pattern can read. A session holding no lease,
the orchestrator or a person at a terminal, writes anything.

**Taking a lease is one-way.** `magus job exec` on a checkout that already holds
a different job is refused. Retaking is how a holder would be graded against
another job's paths, and it costs nothing to a holder that runs its bootstrap
twice: taking the lease it already holds is allowed and does nothing.

**A declared boundary is enforced elsewhere.** Beyond the row ownership above,
this store gates nothing: it records the text an orchestrator put in a holder's
prompt, where a human can read it. The [agent guard](guard.md) is the one reader
that turns it into a verdict. Every uncertainty there fails open with at most an
advisory (no store, an unreadable one, a writer that took no lease) because this
is a seatbelt for a harness that opted in and not a sandbox.

**One set of jobs per repository.** The rows live in one JSON file in the
per-REPOSITORY state directory (`<XDG state>/magus/jobs/<repo>/jobs.json`),
keyed the way [memory](../../../reference/manpage/magus-memory.md) and session
history are keyed: every worktree and every clone of one repository reads one
set. That is what lets an orchestrator declare a plan in its own checkout and a
holder take its lease from another. A store an older magus left behind is
carried forward the first time the new one opens it.

**A read-only job carries an abbreviated row**: `read_only` set, and empty write
and deny paths that then read as deliberate rather than forgotten.

### Two answers the store gives back

Neither is enforcement. Each is something an orchestrator would otherwise derive
by hand from a table it wrote itself, and each leaves the decision where it was.

**Overlaps.** A list reports every pair of live jobs whose `write_paths`
intersect, each side's own declarations kept apart, because they are rarely the
same string and which job claimed which is the part a reader acts on. Derived on
the read and stored nowhere, so it cannot go out of date with the rows. A path
is compared by containment (a job owning `internal/job` overlaps one owning
`internal/job/store.go`) and a glob is judged by the directories it names, which
over-reports rather than misses a pair. A job in a terminal state is in no pair,
because a finished or released job is not competing for anything.

**Releases.** Shrinking `write_paths` is how a job announces it has finished
editing a path, and the store records each dropped path with the digest that
path carried at that moment: the file's sha256, or one of three words when it
cannot be one. `absent` when nothing is there, `dir` for a directory, which has
no single content hash, and `unreadable` for a path that is there and could not
be hashed. `absent` and `unreadable` are deliberately not the same answer: "the
releaser deleted it" and "something is there nobody could read" send you to
different places. Hand the digest to the job taking the path over; one that no
longer matches at verification time means that job built on a tree the releaser
never saw.

Three doors write the store and they reach one set of rules: `magus job` is the
person's, `magus_job` is the agent's, and `magus\job` is a magusfile's.

```sh
magus ls jobs                # every job, parents above the ones they forked
magus ls jobs -o json        # the same records, overlaps included
magus describe job <job>     # one job's terms
```

## Give the holder its terms

The context a delegated agent receives should come from the row, not from the
orchestrating model's recollection of it. `magus describe job <job>` renders
that context and nothing else: the goal and acceptance criteria verbatim, the
write, deny and read paths, the projects those paths reach, the paths a sibling
job is holding and who holds them, the affinity the graph knows about, the
single check that job is allowed to run, and the jobs it depends on. A section
with nothing in it is dropped, which is the mechanism rather than a nicety: `ci`
cannot leak into a holder's terms because nothing renders it. Two renders of one
row are byte-identical, which hand-typed prompts are not.

It REFUSES a job whose check is the gate, or a target that chains to one, and
says so rather than printing terms nobody may act on.

## Take the lease

```sh
magus job exec <job>
```

That takes the lease on the job in this checkout and records the base this tree
landed on beside the checkpoint the job was handed, with the divergence between
them as a fact rather than a refusal. With no job named, it prints the one this
checkout holds.

The base is the half a revision cannot supply on its own. The checkpoint is what
the orchestrator HANDED the job; the base is what the checkout actually LANDED
ON, and hosts that isolate workers in per-worker trees routinely branch them
from an older revision than the tree that was partitioned. Recording it is a
FACT and not a gate: a divergence records, because refusing would leave the
orchestrator with no record that a holder went to the wrong base, which is the
one case the record exists for.

## Wiring the lease into a worker

A declared boundary grades nothing until the writing process says which job it
is acting on, and it says that through its ENVIRONMENT. So the orchestrator that
spawns a worker exports the id into that worker's environment, and the hook
process the worker's host launches inherits it from there:

```sh
export BAGGAGE=magus.lease=<the job id>
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
are attribution, and nothing that names a job, so a worker whose orchestrator
never exported the variable is graded as an editor magus cannot attribute, which
is an advisory rather than a deny and leaves every rule above it inert. A
wrapper that builds its own argv can pass `magus session hook --lease <id>`
instead; an explicit flag wins over the environment.

## What the guard enforces under a lease

Once a worker names a live job, the [guard](guard.md) reads that row on every
file write and every command. It denies:

| the guard refuses                                            | the row field that decided it    |
| ------------------------------------------------------------ | -------------------------------- |
| any write, before the holder takes the lease here            | the recorded base                |
| any write, by a job that gathers evidence and writes nothing | `read_only`                      |
| a write covered by this job's own deny list                  | `deny_paths`                     |
| a write covered by another live job's write list             | `write_paths` (that job's)       |
| a write outside every entry in this job's own write list     | `write_paths`                    |
| a command running the `ci` gate                              | `check`                          |
| a READ of a path outside the projects this job may read      | `read_paths`, else `write_paths` |

It advises on one more: your own path, written from a base that diverges from
the checkpoint you were handed.

The read row is the write lane read the other way. `write_paths` stands in when
`read_paths` is empty, because a holder leased to edit a project was pointed at
that project, and the lane it opens is those projects plus what they declare
`depends_on` (see [the guard's focus rule](guard.md#focus-the-read-lane)). Set
`read_paths` when a holder must READ something it must not WRITE: widening
`write_paths` to open a read is how two holders end up owning one file. Without
a lease taken on the checkout the same rule only advises, which is the opt-in: a
hard read boundary needs somebody to have declared one.

The gate row is the one that surprises people. The gate runs ONCE per branch, in
the orchestrator's tree, after every job lands; a job's `check` is the narrow
target it was assigned, so a holder that reaches for the whole pipeline is
refused and handed its own check instead. A row whose `check` names `ci` owns
the gate and is not refused.

Two absences are boundaries nobody declared rather than boundaries of size zero,
and both scope nothing: an empty `write_paths` on a row that is not `read_only`,
and an empty `check`.

None of that table is visible from the verdict a worker sees, which is exactly
what makes a wrong lease dangerous: a checkout holding an unknown id, a terminal
row, or a live row whose base was never recorded all render as an ordinary
advisory, indistinguishable from a session these rules are actually enforcing
on. `magus doctor`'s **lease-binding** check is the other end of that gap. It
reads the same row the guard would and says, in one line, whether this
checkout's lease is live and therefore actually judged, or names why it is not.

## What the sandbox enforces under a lease

The guard above is a seatbelt for harnesses that opt in: it explains a boundary
and, for a worker, denies the tool call that crosses it. The
[sandbox](../../../concepts/sandbox.md) is the boundary itself, and it reads the
same job row rather than a second declaration, because a boundary written twice
is a boundary that disagrees with itself.

When `sandbox.enabled` is true and the acting lease resolves to a live row with
a `parent` and non-empty `write_paths`, every target run and every `magus buzz`
script in that checkout gets a filesystem WRITE grant of exactly:

- the `write_paths`, resolved as globs against the workspace root (a glob that
  matches nothing grants nothing). A LITERAL path that does not exist yet grants
  the nearest directory above it that does, because a job routinely owns a file
  it was forked to create and the guard already admits that write; only a glob
  keeps the existing-files-only rule, since a typo in a glob is the case that
  rule protects against;
- the workspace cache directory and `$TMPDIR`, which a target needs to produce
  output at all.

Reads are untouched: the row declares a write boundary, and a holder has to read
the tree it is changing. A refusal is recorded on the trail as a
`sandbox_denial` carrying the job id, so a reader can say whose boundary was hit
rather than only that something was blocked.

Nothing narrows for a ROOT job (a row with no parent is the orchestrator, and it
owns the checkout), for a job id that names no live row, for a writable row with
no write paths, or when the sandbox is off. A `read_only` row narrows to the
cache directory and `$TMPDIR` alone, which is the sandbox's reading of the guard
refusing every write under such a job.

Both tiers resolve the acting lease the same way, in this order: an explicit
`--lease`, the `BAGGAGE` a worker inherited, then the job `magus job exec` took
in the checkout. A host runs its hooks from wherever it likes, so the guard
locates that checkout from the `cwd` its hook envelope reports and falls back to
the hook process's own directory only when the envelope carries none.

A deny path inside a write path is refused, and it costs the directory holding
it as well: both this policy and landlock are allowlists with no deny rule, so
the enclosing grant is replaced by grants on its children, and a new file
created beside the denied entry is refused with it.

## Return and verify

A holder returns its job with the result of the work:

```sh
magus job exit <job> --stdin < result.json
magus job exit <job>                         # abandoned, recorded no_return
magus job exit --schema                      # what that result must satisfy
```

The result carries `changed_paths`, the `validation` the holder ran as
`{command, output_ref}`, the `descendants` it forked, and its
`unresolved_risks`, under a required `schema_version`. The run behind the output
ref is resolved in the holder's OWN checkout and its record is filed alongside,
because the output store belongs to that checkout and nobody else can reopen it.
That is what makes the evidence portable when the parent waits from another
worktree.

There is no `passed` field. A holder's verdict on its own run is the assertion
the ref exists to replace. Do the work first, then file the result: it is a
record of what happened, not a form to fill in while you are still deciding what
to do.

```sh
magus job wait <job>
```

`wait` verifies the filed result against the job it was handed, and it verifies
EVIDENCE rather than what the result claims: every changed path inside the write
paths and outside the denied ones, a change set that is not empty, the
descendants the store carries, and a run recording a PASSING execution of this
job's own check. A job that verifies is recorded `pass`.

Two failing statuses, because they send a caller somewhere different. Exit 1 is
a status: the result was read and rejected, and every rule that failed is named.
Exit 2 is magus unable to answer: nothing was filed and nothing was piped in,
the result would not decode, or the job would not write.

It checks what is mechanical. Whether the work is GOOD, and whether the job's
acceptance criteria are met, stay the reading of whoever forked it.

## Watch it: the console Jobs view

The [console](../../../reference/console.md) draws one Jobs view, because a job
is ONE KIND OF THING however it was created. The daemon holds its own
maintenance jobs (graph sync, trail rotation, the review check) and a session
holds the ones an orchestrator handed out; both list together, and a HOLDER
column reading `daemon` or `session` is what separates them. `magus ls jobs`
prints the same two sets, with the same column, as a tree with parents above the
jobs they forked.

A job in a reported overlap is marked on both rows. A live row carries how long
since it was last touched, and the released paths and their digests read in the
detail beside the row. No row transitions itself: every state was written by an
agent or a person, which is why a row that has gone quiet is a job YOU decide is
possibly dead.

The service behind it is `magus.job.v1alpha1.JobService`, the daemon's one
mutating console surface, mounted behind the same loopback bind and bearer token
as everything else. Start it with `magus server start`; see
[the daemon](../daemon.md). `magus server status` prints the mcp and console
URLs, and says so explicitly when the daemon predates the tree, because every
call through an older daemon is answered by the older build.

## The spawn is recorded, never judged

Wire your host's sub-agent tool to the same `magus session hook` call as the
rest of [the guard](guard.md). A payload carrying a `prompt` rather than a
command or a file path is a delegation: magus records it as an `agent_spawn`
event on the local Activity Trail and returns `pass` without evaluating a single
rule.

That exemption is the point, not an oversight. There is no command and no path
to judge, only a context transfer to note, and a prompt that merely MENTIONS a
denied command would otherwise block the job that describes it. The handed
context is routinely kilobytes, so it lands as a content-addressed blob and only
its reference rides the event.

magus does not switch on your host's tool name anywhere: a payload carrying a
prompt IS a spawn, and the callee label the host supplies becomes the event's
action so a page of them groups by what was spawned.

Joining an event to a job is cooperative. Nothing in a host event names a magus
job and magus will not infer one from prose, so the job is stamped only when the
handed context's FIRST non-blank line reads:

```text
lease: <id>
```

Use the same id you forked. An orchestrator that wants the join writes the
marker; one that does not gets an event with no job, which is a missing join
rather than a wrong one. A marker line quoted deeper in a prompt stamps nothing,
on purpose.

## Verify against the diff

A holder's report of what it changed is a claim. The checkpoint is what turns it
into something you can check.

1. Diff the actual tree against the job's checkpoint, and compare THAT against
   the row's write and deny paths. `magus graph diff --rev <revision>` gives the
   domain-level answer; `git diff <revision> | magus diff -` annotates each
   changed file with its reach, public-surface exposure, and referents. See
   [`magus diff`](../../../reference/manpage/magus-diff.md): it refuses a git ref
   given positionally, so the pipe is the sanctioned spelling.
2. Check the dirty half of the token. A checkpoint whose digest differs from the
   tree you are diffing means the holder saw a different uncommitted tree, and
   the comparison you are about to make is not the one you think.
3. Take acceptance evidence as an [output reference](../../../concepts/cache/output-refs.md)
   the orchestrator reopens, never a holder's prose. A holder that ran a filtered
   subset and one that quietly restated its criteria both report success, and a
   transcript cannot tell you which happened.
4. Regenerate declared outputs once, centrally, after the source work converges,
   then run the release gate yourself.

The same object serves review time. If you recorded a checkpoint when you
stopped reading, the delta since then is the incremental-review flow on the
[agents hub](../agents.md#incremental-review): handing work out and picking
review back up read the same identity.

## What magus never does here

- Block a writer it cannot attribute. Only a process that named a live job is
  graded against a declared boundary; anyone else editing this workspace is
  advised at most, because a human in their own checkout names no job either.
- Transition a row, or derive a state or a completion from one. Every state was
  written by the agent or the person that put it there.
- Judge a delegation prompt, or let one change a guard verdict.
- Mint anything for a checkpoint: no tag, no stash, no ref, no file.
- Inject any of this into an agent's context. Every surface here is pull-based,
  and the [knowledge graph](../../../concepts/knowledge.md) the partition is
  argued from is read the same way.
