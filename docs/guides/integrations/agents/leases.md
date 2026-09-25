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
job: the write and read paths that job declared, enforced in the checkout that
took it. A job is the thing; a lease is permission over it.

How to split the work is not on this page. That is the
[`magus-multi-agent` skill](../../../reference/skills/magus-multi-agent.md):
partition by write set rather than by affected project, prove the jobs cannot
collide, narrow the scope at every level, match a model to each job. This page is the surface
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
  --criteria 'move the store' \
  --write-paths internal/job/store.go,internal/job/store_test.go \
  --check 'test internal/job'

magus job fork --stdin < job.json   # the same job as a record
magus job fork --schema             # what that record must satisfy
```

Write paths name files. An existing directory is refused
([MGS3018](../../../reference/codes/sandbox/MGS3018.md)) unless it is the root
of a project the job owns whole, or does not exist yet because the job creates it.

A row carries `id` and optionally `parent` (the job this one was forked from),
`criteria` (the prose half; the machine-checkable half is `completion_gates`),
`checkpoint` (as `magus vcs checkpoint -o name` prints it), `write_paths`,
`deny_paths`, `read_paths`, `depends_on`, `model`, `check`, `state`, and
`read_only`. The
store adds `schema_version`, the actor that recorded the row, `created`,
`updated`, `releases`, `unattributed` (paths this job owns that somebody
outside it wrote, noticed by the guard), and `write_proof`, all output-only: a
timestamp a client sent would be a fact about that client's clock.

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
boundary, changing the plan's shape, and verifying a result are the forking
session's. The rule lives in the store rather than in a guard pattern because
the CLI, the `magus_job` MCP tool and `magus\job` all reach the same file and
only one of them is a command a pattern can read. A session holding no lease,
the orchestrator or a person at a terminal, writes anything.

**Taking a lease is one-way.** `magus job exec` for a session that already holds
a different job is refused. Retaking is how a holder would be graded against
another job's paths, and it costs nothing to a holder that runs its bootstrap
twice: taking the lease it already holds is allowed and does nothing.

**`magus job exec --vacate` gives the binding up.** One-way means one-way until
something releases it, and nothing did: the marker outlives the job it names,
so a checkout bound to one that finished (exited, passed, failed, or was never
returned) stayed stuck until a person deleted the file by hand. `--vacate`
clears it instead, refusing only while the job is still `declared` or
`running` - walking away from those two would leave the checkout's next write
ungraded. A job already exited, one the store no longer carries, or no binding
at all all vacate cleanly, and a checkout with nothing bound reports that
rather than erroring. It releases only the SESSION's own binding, so a sibling
session working in the same checkout keeps its lease.

**A workspace-load file needs a worktree of its own.** `fork` refuses a job
whose `write_paths` cover a file magus must READ to load the workspace - any
project's `magusfile.buzz` or `magusfiles/*.buzz`, its `magus.yaml`, and the
workspace-local spell sources those magusfiles import - while another live job
with write paths is already bound to the same checkout. The refusal names the
file and the job that holds the checkout, and the fix it names is a worktree
rather than a narrower boundary. Half-saved, one of those files stops the workspace
loading for EVERY worker in the checkout at once: they lose `magus run`, `magus
ls` and their own tests, over an edit none of them made and none of them can
see. The same three doors are covered, since `magus job fork`, `magus_job` and
`magus\job.put` share one declaration path.

Nothing else about a shared checkout is refused. Two sets of write paths that
merely overlap are the orchestrator's call - it may have sequenced them
deliberately - so the fork RECORDS what it could prove instead, in `write_proof`:
`alone` when no other live job with write paths was bound to the checkout, else
`disjoint` or `overlapping`. `magus ls jobs` prints it per row, so a plan read
later says which forks were checked and which were not.

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

**A timeout is optional.** `magus job fork <job> --timeout 2h` (or `timeout` in
the `--stdin` record or the `magus_job` call) has the store stamp a `deadline`
on the row at fork time; nothing accepts a deadline directly. It is unset by
default, and a job with acceptance criteria needs no bound. Past the deadline the
guard denies every write graded under that lease and its write paths stop
blocking other jobs, while the row stays live: magus never transitions it, and
`magus ls jobs` marks it `overdue`.

**Limits exist only when the workspace sets them**, in magus.yaml:

```yaml
jobs:
  max_depth: 3 # levels below the root job; 0 = unlimited
  max_live: 12 # live jobs under one root; 0 = unlimited
  default_timeout: 2h # applies when fork names no --timeout
  stale_after: 30m # flag live jobs untouched this long
```

Every key is unset by default. `magus job fork` refuses past `max_depth` or
`max_live`, naming the key that set the limit.

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

**Jobs nobody is waiting on.** A list also marks a live job an `orphan` when its
root job has ended, and `stale` when it was not updated within
`jobs.stale_after` (only when that key is set), naming `magus job exit <id>` for
each. `magus doctor`'s **job-tree** check reports the same two. Both are
reports: ending the row stays yours.

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
magus job exec <job> --session <the host's session id>
```

That takes the lease on the job in this checkout and records the base this tree
landed on beside the checkpoint the job was handed, with the divergence between
them as a fact rather than a refusal. With no job named, it prints the one this
checkout holds.

**A lease binds per SESSION, not per checkout.** `--session` names the session
taking it, as the agent host names the conversation to its own hooks, and the
binding is that session's. So several workers sharing one checkout each hold
their own lease, each has its own write paths graded, and each is denied outside them.
Without it the binding is the whole checkout's, which is what every binding was
before: a session that reports none, and a session nobody bound, both read the
checkout-wide marker, so a worktree bound by hand still grades the sessions
inside it. A session that HAS its own binding never reads the checkout-wide one,
which is the boundary that matters - a worker bound to one job cannot silently
act under another.

Pass the same id your host reports to the guard hook, or the two halves bind and
grade under different names. Where the host reports no session, the
checkout-wide fallback is the honest answer: one worker per checkout, which is
the arrangement the worktree rule asks for anyway.

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
wrapper that builds its own argv can pass `magus shell --lease <id>` instead;
an explicit flag outranks every other source.

## What the guard enforces under a lease

Once a worker names a live job, the [guard](guard.md) reads that row on every
file write and every command. It denies:

| the guard refuses                                            | the row field that decided it    |
| ------------------------------------------------------------ | -------------------------------- |
| any write, before the holder takes the lease here            | the recorded base                |
| any write, once the job's timeout has passed                 | `deadline`                       |
| any write, by a job that gathers evidence and writes nothing | `read_only`                      |
| a write covered by this job's own deny list                  | `deny_paths`                     |
| a write covered by another live job's write list             | `write_paths` (that job's)       |
| a write outside every entry in this job's own write list     | `write_paths`                    |
| a command running the `ci` gate                              | `check`                          |
| a READ of a path outside the projects this job may read      | `read_paths`, else `write_paths` |

It advises on one more: your own path, written from a base that diverges from
the checkpoint you were handed.

Whatever the row says, it also refuses `magus agent harness apply`, `install`
and `remove`: the hook wiring is what grades the holder, so only an unbound
caller rewires it. The commands refuse themselves under the checkout's binding
or a `BAGGAGE` claim; the guard adds the subagent and session sources a command
cannot see.

A denial for another job's path also says how long ago that job was last
updated and names `magus job exit <id>`, which releases a job nobody holds any
more. A job past its deadline owns nothing against other jobs.

The read row is the write paths read the other way. `write_paths` stands in when
`read_paths` is empty, because a holder leased to edit a project was pointed at
that project, and the boundary it opens is those projects plus what they declare
`depends_on` (see [the guard's focus rule](guard.md#focus-the-read-boundary)). Set
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
on. `magus doctor`'s **bound-lease** check is the other end of that gap. It
reads the same row the guard would and says, in one line, whether this
checkout's lease is live and therefore actually judged, or names why it is not.

## What the sandbox enforces under a lease

The guard above is a seatbelt for harnesses that opt in: it explains a boundary
and, for a worker, denies the tool call that crosses it. The
[sandbox](../../../concepts/sandbox.md) is the boundary itself, and it reads the
same job row rather than a second declaration, because a boundary written twice
is a boundary that disagrees with itself.

When `sandbox.mode` is not `off` and the acting lease resolves to a live row with
a `parent` and non-empty `write_paths`, every target run and every `magus buzz`
script in that checkout gets a filesystem WRITE grant of exactly:

- the `write_paths`, resolved as globs against the workspace root (a glob that
  matches nothing grants nothing). A LITERAL path that does not exist yet grants
  the nearest directory above it that does, because a job routinely owns a file
  it was forked to create and the guard already admits that write; only a glob
  keeps the existing-files-only rule, since a typo in a glob is the case that
  rule protects against;
- the workspace cache directory and the sandbox's private temp dir (every
  child's `TMPDIR`), which a target needs to produce output at all.

Write grants outside the checkout, such as `/dev/null` and the tool caches, are
kept. Reads are untouched: the row declares a write boundary, and a holder has to read
the tree it is changing. A refusal is recorded on the trail as a
`sandbox_denial` carrying the job id, so a reader can say whose boundary was hit
rather than only that something was blocked.

Nothing narrows for a ROOT job (a row with no parent is the orchestrator, and it
owns the checkout), for a job id that names no live row, for a writable row with
no write paths, or when the sandbox is off. A `read_only` row narrows the checkout to
the cache directory and the private temp dir alone, which is the sandbox's reading of the guard
refusing every write under such a job.

Both tiers resolve the acting lease the same way, in this order: an explicit
`--lease`, the job magus recorded the calling subagent was spawned for, the job
`magus job exec` took in the checkout, then the `BAGGAGE` a worker inherited.
Every tier but the last is a record; `BAGGAGE` is the worker's claim about
itself, so it answers only when no record does, and a checkout whose binding
disagrees with it grades under the binding. The verdict names the tier that
answered as `lease_from`: `flag`, `agent`, `marker`, `env`, or `contested` for a
binding that overruled a different claim.

A host runs its hooks from wherever it likes, so the guard locates that checkout
from the `cwd` its hook envelope reports and falls back to the hook process's
own directory only when the envelope carries none.

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

For a job that writes, the claimed `changed_paths` are held to the diff magus
observes since the job's checkpoint: every claimed path must appear in it, at
least one change in it must fall inside `write_paths`, and a diff magus cannot
read fails the job rather than passing it. A read-only job has no diff to check.
`wait` also refuses `pass` while any descendant (by `parent` chain) is still
live, naming each, and grades a job against its ancestors' symbol gates as well
as its own.

Two failing statuses, because they send a caller somewhere different. Exit 1 is
a status: the result was read and rejected, and every rule that failed is named.
Exit 2 is magus unable to answer: nothing was filed and nothing was piped in,
the result would not decode, or the job would not write.

It checks what is mechanical. Whether the work is GOOD stays the reading of
whoever forked it. How much of the job's acceptance criteria magus checks for
you is the next section's subject.

## Completion gates

A job's acceptance criteria are prose a person grades. A **completion gate** is
the part magus grades itself, and `magus job wait` will not record `pass` until
every one verifies. Declare them when the job is forked:

```sh
magus job fork api/migrate \
  --criteria "move the accounts table to the new schema" \
  --write-paths 'db/**,api/**' \
  --gate-check green='go-test api' \
  --gate-paths migration='db/migrations/**' \
  --gate-symbol-unreferenced unused='LegacyAccountStore'
```

A gate names a **kind** (what it examines) and an **expect** (what must be true
of it). Two fields rather than a kind per pair, so `absent` means the same thing
of a file and of a symbol:

| kind     | expect                                         | proven by                                                                   |
| -------- | ---------------------------------------------- | --------------------------------------------------------------------------- |
| `check`  | `passed`                                       | a recorded, PASSING run of that target, captured after the job was declared |
| `paths`  | `changed`, `present`, `absent`                 | the diff since the checkpoint, or the tree as it is now                     |
| `symbol` | `changed`, `present`, `absent`, `unreferenced` | the knowledge graph, at the granularity below a file                        |

Each kind has a natural expectation, so the common gate declares only its
subject: a check is asked whether it passed, files and symbols whether this job
changed them. The flags spell the others out (`--gate-paths-present`,
`--gate-symbol-absent`, `--gate-symbol-unreferenced`), and `magus job fork -h`
lists them.

Every kind reads something magus already holds, which is what separates a gate
from an attestation. There is deliberately no escape hatch for "this command
exited 0": magus did not record that run and cannot attribute it, so such a gate
would be the easiest of all to satisfy falsely. Declare a target and use `check`.

`symbol` + `unreferenced` is the one worth knowing about: the symbol may still
exist, and nothing may name it. That is the remainder a partitioned rename leaks.
Split the work per project and the callers that live in no project belong to no
job, so every job passes and the rename is unfinished.

The single `--check` is one of these gates, under the id `check`.

A gate is graded against what magus observes and never against the result's own
`changed_paths`. The holder's account of its work is the thing the gate replaces,
so a result claiming a file the tree does not carry is rejected, and the refusal
names each subject that failed rather than only that the gate failed:

```text
rejected api/migrate, and its state is unchanged
  completion gate "migration": nothing matching "db/migrations/**" changed since
  4a1c0cc8, so this gate is unmet
```

An observation magus could not make FAILS the gate: an unreadable diff, a symbol
graph that will not open. That is the opposite of how the guard treats an
unanswerable question, and deliberately: a guard that cannot ask must not refuse
a person's own command, while a gate that cannot verify must not certify, or the
cheapest way past it is to break the observation.

### Asking where a job stands

`magus job wait` verifies and RECORDS. To ask the same question without advancing
anything:

```sh
magus describe job api/migrate --gates
```

It grades every gate against the evidence magus holds right now, writes nothing,
and exits 1 while any gate is unmet. Because it runs the same grading `wait`
does, the two cannot disagree; because it records nothing, an orchestrator may
ask while the holder is still working, and asking never blocks that holder. The
gates that read the tree and the graph answer even for a job that has filed no
result at all.

Sequence gates with `depends_on` between them when one has to be cleared before
another is approached. A failed prerequisite propagates, and the declaration
refuses a cycle. Gates do not nest: one wanting children is a JOB wanting
splitting, which the multi-agent skill's "every level narrows" rule already
covers, and keeping gates flat leaves the job tree as the only hierarchy with an
owner.

A gate may not name the release gate. `--gate-check ci` is refused by the same
rule that refuses `--check ci`, for the same reason: the gate runs once, in the
forking session's tree, after every job lands.

### Declaring gates from Buzz

A magusfile or a `magus buzz` script declares them through the same store, as
records rather than flags:

```buzz
import "magus";
import "std";

fun declare() > void {
  try {
    magus\job.put("api/migrate", {
      "criteria": "move the accounts table to the new schema",
      "write_paths": ["db", "api"],
      "completion_gates": [
        {"id": "green", "check": {"target": "go-test", "project": "api"}},
        {"id": "migration", "kind": "paths", "paths": ["db/migrations/**"]},
        {"id": "old-gone", "kind": "symbol", "expect": "unreferenced", "symbols": ["LegacyAccountStore"]},
      ],
    });
  } catch (e) {
    std\print("could not declare the job: {e}");
  }
}
```

`kind` defaults to `check` and `expect` to the kind's natural expectation, so
the first gate above needs neither. The records are validated on the way in: a gate
carrying both a check and paths is refused, and so is one naming paths nothing
could ever match.

## Watch it: the console Jobs view

The [console](../../../reference/console.md) draws one Jobs view, because a job
is ONE KIND OF THING however it was created. The server holds its own
maintenance jobs (graph sync, trail rotation, the review check) and a session
holds the ones an orchestrator handed out; both list together, and a HOLDER
column reading `server` or `session` is what separates them. `magus ls jobs`
prints the same two sets, with the same column, as a tree with parents above the
jobs they forked.

A job in a reported overlap is marked on both rows. A live row carries how long
since it was last touched, and the released paths and their digests read in the
detail beside the row. No row transitions itself: every state was written by an
agent or a person, which is why a row that has gone quiet is a job YOU decide is
possibly dead.

The service behind it is `magus.job.v1alpha1.JobService`, the server's one
mutating console surface, mounted behind the same loopback bind and bearer token
as everything else. Start it with `magus server start`; see
[the server](../server.md). `magus server status` prints the mcp and console
URLs, and says so explicitly when the server predates the tree, because every
call through an older server is answered by the older build.

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
