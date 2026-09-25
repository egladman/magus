---
title: magus-multi-agent
generated_from: internal/agent/skills/magus-multi-agent/SKILL.md
description: "Split work across agents in a magus workspace as an acceptance-criteria loop: partition by WRITE SET using graph evidence (magus refs --occurrences, explain, affected --plan --stdin), prove the leases cannot collide, narrow the scope at every level, and match each lease's model to the work it needs."
tags: [agents, skills, magus-multi-agent]
skill_full_bytes: 38909
skill_short_bytes: 29214
---

# magus-multi-agent

Split work across agents in a magus workspace as an acceptance-criteria loop: partition by WRITE SET using graph evidence (magus refs --occurrences, explain, affected --plan --stdin), prove the leases cannot collide, narrow the scope at every level, and match each lease's model to the work it needs. Use when a change needs several disjoint groups of files edited, when an audit or review covers a tree, or when the user says "fan this out" or "spin up an agent per package" - you do not need to be asked. Do NOT fan out one coherent edit just because it invalidates many projects: a shard plan partitions VALIDATION, not editing, so it can veto a fan-out but never license one.

Install it, rather than copying from this page:

```sh
magus agent install .claude/skills   # writes both forms below
```

An installed copy carries a provenance stamp, so `magus doctor` can tell you when a magus upgrade has made it stale. Text copied from this page carries none.

## What an installed copy carries

`magus agent install` writes this frontmatter above the body. `magus doctor` reads it to report whether your installed skills are current.

| field | value |
| --- | --- |
| `license` | `GPL-3.0-or-later` |
| `compatibility` | `any-agent` |
| `source` | `magus` |
| `agent-skill-version` | `86` |
| `knowledge-schema-version` | `15` |
| `skill-content` | `596c6ff1d156` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both forms below report it: they go stale together, never one silently, and a change to another skill does not move it.

## The two forms

Both are hand-authored from one source body. The short form is the always-loaded primary - the enumeration dropped, the judgment kept, for the most capable readers rather than the least. The full form is its `<name>-full` twin, loaded by name when a reader wants the rationale. The bar above shows how much shorter the primary is; switch between them here to see exactly what it gave up. See [Skills](../../guides/integrations/agents/skills.md) for how to choose.

<article class="landing-tabs">
<header>
<input type="radio" name="magus-multi-agent-variant" id="magus-multi-agent-tab-short" checked>
<label for="magus-multi-agent-tab-short">Short form</label>
<input type="radio" name="magus-multi-agent-variant" id="magus-multi-agent-tab-full">
<label for="magus-multi-agent-tab-full">Full form</label>
</header>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-multi-agent/SKILL.md
```

````markdown
# Splitting work across agents

Count the WRITE SETS your change needs - the distinct groups of files that must be
edited - not the projects a change invalidates. That distinction decides everything
here, and getting it backwards is the standard way fan-out goes wrong: a one-line
edit in a central package invalidates half the workspace and is still one edit.

`magus affected <target> --plan` partitions VALIDATION - which targets to run,
grouped for runner balance. It is not an edit assignment and not a proof of write
isolation (the section below is what establishes that). So it can veto a fan-out
and never license one: one shard means keep the work local; several shards mean the
testing parallelizes, and whether the editing does is still your question to answer.

Before any edits exist there is no diff to plan, so the shard plan is empty and
proves nothing. Finding the candidate paths IS the partitioning work, and it is
done with the graph:

```sh
magus graph build                           # first in a fresh worktree, or refs answers "unknown, not absent"
magus refs <symbol> --occurrences -o json   # every edit site, column-precise
magus explain <node>                        # one node's edges and blast radius
magus affected ci --plan --stdin            # plan PROPOSED paths, before editing
```

You do not need to be asked.

Fan-out is not inherently expensive. Say what a
round will cost when the user is deciding, and prefer the smallest fan-out that
covers the work.

Fan out only after the collision check below REPORTS the jobs disjoint; write
sets that look separate is not that check. If the graph supports only one
coherent write set, keep the work local - fanning out one job adds coordination
and buys nothing. The root agent owns the goal, the budget, the topology,
integration, and final verification, and never hands those out.

## Coalesce what the write sets allow

Disjoint write sets LICENSE parallelism; they do not require it. Every worker
carries a fixed load before it reads a line of the diff. So the question after
partitioning is never "may these run in parallel" but "is each one big enough to
be worth a worker".

Four rules, in the order they bite:

- A depends-on chain is ONE worker in sequence, not two workers in turn.
- Merge small disjoint units inside one project. The wall-clock gain from
  splitting them is usually smaller than the load you pay twice.
- Spawn only when more than one independent BIG unit survives the merge. One unit
  is inline work.
- Fill idle root time from that pool, never by splitting finer. A root blocked on
  a gate is a reason to start the next merged unit.

## Run the graph-engineering loop

Treat graph engineering as
an acceptance-criteria loop with graph-derived jobs: define, partition,
hand out, observe, evaluate, course-correct, integrate. A worker is not complete
until its criteria and assigned check pass; the root agent decides whether
the top-level goal is complete.

## Declare the interface before any job forks

When a change adds a shared surface - a module several call sites will use, a type,
an event, an exported function - the ROOT names it before any edit: the path, every
exported name with its signature, and the EXISTING symbols it must reuse rather than
restate. Workers implement names they were handed and never coin a public one. A
name invented at each call site is how one concept ends up with five spellings.

Then make the declaration gradeable, so the names are a contract rather than a
suggestion:

- `symbol` + `present` for each new exported name.
- `symbol` + `absent` for the name a second copy would predictably take beside a
  symbol the work must reuse. No gate grades reuse itself.
- `symbol` + `unreferenced` for each helper the shared surface replaces.

Check each declared name with `magus refs` before forking. A bare name that
resolves to more than one definition is graded against one of them, silently.

This applies with zero workers. A solo change that crosses several call sites is a
one-job tree, and skipping the declaration because nothing is being forked is the
same failure without a fork to blame.

## Set one topology boundary

Before spawning, state the topology: the model per job and whether isolated
worktrees are available. Fan-out and depth are not capped unless the workspace
sets a cap: spawn as many jobs, nested as deep, as the partition supports. A limit
exists only when magus.yaml's `jobs` section sets one (read it with
`magus config view` or `magus_config_get`): `max_depth` and `max_live`
make `magus job fork` refuse past them, naming the key, and
`default_timeout` bounds a fork that names no `--timeout`. Honor a cap the user
states the same way. Editing costs the workspace nothing; what contends is VALIDATION - the
magus runs a job triggers - so read the live pool (`magus status`) before
starting a validation rather than before starting an agent, and serialize
validations that share a worktree even when their write sets are disjoint.

Assign the check from the pipeline the workspace composed, not from convention:
`magus describe target ci <project>` names what `ci` chains and in what order,
so a job gets the narrowest target from that decomposition and the integrator
re-runs the described order, with `magus affected ci` re-proving the whole
composition.

A job's check is that narrow target and never the gate; the guard reads
the row and denies `ci` to any worker whose check names something else. The
root gates ONCE, in its own tree, after every unit lands.

Decide each job's validation PLANE with its target. A worker environment that
cannot execute magus at all - an isolated tree with no usable binary, or a
guard that routes raw language tools back to targets it cannot run - cannot
validate anything it writes. Mark that job's check ROOT-DEFERRED when you fork
it: the worker writes the tests, stops at the static checks
its environment does run, and says so; the root executes the job's target
centrally before verifying.

A worker may fork part of its own job. What it may not do is hand it out
without shrinking the problem. These rules give
it a definitive end without capping how deep it goes:

- **Every level narrows.** A child's scope is a strict subset of its parent's. A
  worker that would hand on its whole job should do the work instead.
- **Every job carries acceptance criteria down with it.** A child inherits its
  parent's criteria plus its own. A job nobody can evaluate is a job that cannot
  end, which is what makes depth dangerous rather than the nesting itself.
- **A job that fails its criteria twice is not re-issued.** The root does it
  locally, or serializes it behind whatever keeps breaking it.
- **Whatever the parent does not hand out, the parent still owns.** A strict subset
  leaks by construction: split "no caller of X remains" into per-project jobs and
  the callers in no project belong to nobody, so every job passes and the goal is
  unmet. Carry a remainder row at each level and close it explicitly.

Pick the model that FITS the job, and SAY which one. That is the whole rule, and it
runs both ways: a mechanical rename does not need the strongest model available, and
an ambiguous API boundary does not get the cheapest one because it looked like less
work.

Naming it is the half that is checkable. Every spawn names a model, or names an agent
definition that names one. Inheriting the parent's model ON PURPOSE is fine and often
right, since a hard review under a cheaper coordinator is exactly the case an ordering
rule would forbid. Inheriting it BY OMISSION is the failure: a host whose default is
"same as the parent" turns every unnamed spawn into the most expensive one available,
and nothing afterwards records that no choice was made.

ASK THE HUMAN when the right model is unclear, before spawning rather than after the
budget is spent. There is no ordering rule to fall back on: "only ever spawn something
weaker" was tried and withdrawn, because same-strength offload is legitimate. So an
unclear case is a question, not a default.

Model NAMES belong to the host, never to magus: they change faster than any table here
could track. Name the model your host names, or point the spawn at an agent definition
the user owns. Where the host has a default-subagent setting, setting it is what makes
omission cheap instead of expensive.

Map work to provider capabilities without assuming model names:

| Model | Assign |
|---|---|
| principal | architecture, ambiguous ownership, public APIs, migrations, security, integration |
| standard | isolated implementation with a clear contract and bounded project surface |
| economy | mechanical edits, fixtures, docs, inventory, and read-only evidence gathering |

If the host cannot select models or reasoning effort, keep its default. Tool surface
is a separate axis from the model: evidence gathering, scouting, and review get a
read-only tool surface where the host offers one. Never downgrade the root
integration pass or final release gate.

Nesting is allowed when the host supports it, but it does not create a
new budget or a private ownership map. Before a child spawns descendants, it must
report the proposed jobs to its parent. The store must then carry those
descendants, their parent, model, criteria, and write paths. Descendants
inherit the ancestor's deny paths and may subdivide only the ancestor's
write paths. A cap the user sets applies to the whole tree, not once per parent.

Keep one integration owner at the root even when the job tree is deep.
A child coordinates its descendants but may not relax the root's criteria.

## Seed the partition with Magus

Choose the target that will validate the work. `ci` is the release gate, but any
target accepted by `magus affected <target>` can be planned:

```sh
magus affected <target> --plan
```

For a proposed change whose paths are known but not edited yet, plan those paths
instead of the current diff:

```sh
printf '%s\n' <repo-relative-path>... | magus affected <target> --stdin --plan
```

Read the JSON fields `count`, `max_parallel`, `source`, and `matrix`. A shard is a
history-balanced execution group, not an edit assignment or proof of write
isolation. Use it only as the first partition. If paths are not known, query the
task's symbols, files, and projects before producing a stdin plan.

## Prove that jobs do not collide

Classify the union of every job's proposed paths in one call:

```sh
magus describe file <both jobs' paths>... -o json
```

Read the facts: `overlaps` lists each declaration covering more than one
proposed path - a shared write set by construction; `claims[].target` names the
target that regenerates a path (generated outputs have one integration owner,
never hand-edited by workers); `depends_on` carries the owner's direct edges.
Affinity stays with `magus_insight lens=affinity`, and `magus refs <symbol>`
when two jobs may touch the same API. A read-only job has no write set, so it is outside
this analysis entirely.

Two jobs may run together only when:

- The combined classification reports no overlaps - source write sets and
  declared outputs disjoint.
- Neither consumes an API or generated artifact the other will change.
- Shared manifests, lockfiles, schemas, workspace configuration, and agent
  instructions have one owner.
- Dependency and temporal-affinity evidence does not indicate that they should
  move together.

ONE WORKTREE PER WORKER wherever a write path touches workspace configuration - any
project's magusfile, its `magus.yaml`, or a spell source a magusfile imports.
Those are the files magus READS to load the workspace, so a half-saved one is
not a conflict between two workers: it stops the workspace loading for everybody
in that checkout at once, and the rest lose `magus run`, `magus ls` and their own
tests over an edit they cannot see.
`magus job fork` refuses such a job outright while another live job with
write paths is bound to the same checkout, naming the file and the holder. A
separate worktree is the answer, not a narrower boundary: the file has one owner, so
the only way both jobs can be right is for one of them to be somewhere else.

Fork records what it could prove in `write_proof` - `alone`, `disjoint` or
`overlapping` - and `magus ls jobs` prints it. An overlap is recorded rather than
refused, because sequencing two jobs onto one path is a call only you can make;
what the row settles is whether anybody checked.

Spawning into a checkout that already holds a live job carries the same
reminder from the guard, once per session, with the union of the write paths to
classify. Answer it by running the call above or by handing the new worker its
own worktree - a proof or a worktree, not a judgment that it looks fine.

Project boundaries alone are insufficient. A declared dependency means group the
work or serialize producer before consumer. Treat strong hidden affinity as a
warning. When evidence is incomplete, reduce parallelism.

## Fork one job per unit of work

Before spawning, fork one job per unit - including the checkpoint it was handed
(`magus vcs checkpoint -o name`: the revision, plus a dirty-patch digest when the
tree is not clean) - and keep descendants in the same store. Fork each one with
the `magus_job` tool from the orchestrating agent, or `magus job fork` from a
person at a terminal:

| Job | Parent | Checkpoint | Criteria | Completion gates | Write paths | Deny paths | Read paths | Depends on | Model | Check | State |
|---|---|---|---|---|---|---|---|---|---|---|

Render the prompt FROM the row rather than typing it: `magus describe job <job>`
prints the job's own criteria, boundary and check, plus what the workspace knows
and nobody wrote down. Two renders
of one row are byte-identical, which hand-typed prompts are not. It
REFUSES a job whose check is the gate, or a target that chains to one.

Every worker prompt must include its row, its JOB ID, relevant graph
evidence, and the global spawn rule. Require the worker to export
`BAGGAGE=magus.lease=<its id>` before it works - the guard grades its writes only when that is
set. Require it to preserve
unrelated changes, stay inside write paths, avoid generated outputs, run only its
assigned Magus target, and return its result with `magus job exit --stdin` in the
schema `magus job exit --schema` prints: changed paths, the validation it ran with
the output ref that proves it, descendants it forked, and unresolved risks. A typed
result is what makes verification mechanical. Keep unresolved risks mandatory, so a
mis-scoped worker can say so instead of widening silently.

Taking a lease narrows what a worker may write in the store, never what it may see. Its
own row accepts four writes: recording the base it landed on, shrinking its own
write_paths (how it releases a path), ending itself in fail or no_return, and
forking a child inside its own write paths. Everything else, including any other field
on its own row and any write to a row that is not its own or its child, is the
orchestrator's alone.

The checkpoint you recorded is what you HANDED the job; the base it
actually LANDED ON is a separate fact, because hosts that isolate workers in
per-worker trees routinely branch them from an older revision than the tree you
partitioned. A worker's first
required act is `magus job exec <its id>`, which takes the lease in that checkout
and records the base it landed on. Where the host names its session, the worker
passes `--session <that id>` and the lease is the SESSION's rather than the
checkout's, so workers sharing a tree each hold one and each has its own write
paths graded.
Pass the same id the host reports to its guard hook, or the two halves bind and
grade under different names. The
answer is a status recorded on the row - match, revision-match (same revision,
different uncommitted patch), diverged, or unknown - plus a reading of it that
names both tokens and the next step. It is a FACT and not a gate: every status
records, diverged included. Acting on it is still yours: respawn from the right
revision, or have the worker materialize the files it builds on from the intended
one (`git show <rev>:<path> > <path>`, verifying each blob against
`git rev-parse <rev>:<path>`) and re-fork the job with the right checkpoint.
Also name any fact that will READ as drift to the worker's snapshot - a project
deleted this session, a rename, an index regenerated underneath it - never a
generic "expect drift" line.

Write paths are the WRITE boundary. The guard reads them as a READ boundary too, so a
worker leased to `apps/web` is advised off `apps/admin` and denied it outright
once `magus job exec <its id>` takes the lease in its checkout. When a worker must READ something it
must not WRITE, put that path in the row's `read_paths` instead of widening
`write_paths`: one list cannot say both, and widening the write paths to open a
read is how two workers end up owning one file. `read_paths` is the only widening
there is; nothing in the environment turns the rule off.

Ownership ends when EDITING ends, not when the worker exits. A worker that has
finished writing a contested path announces the release immediately - shrink the
job's `write_paths` with another `magus_job` write, or message the orchestrator
if the host supports it - and then carries on validating.

That write records each dropped path with the digest it carried at that moment.
Hand the digest to the job taking the path over: it names the version being
inherited, and one that no longer matches at verification means the waiter built
on a tree the releaser never saw.

Advance the row on every state change. `magus ls jobs` then answers two questions you
would otherwise derive by hand: which live jobs claim intersecting
`write_paths`, and how long since each row was touched. Both are facts, not
verdicts - magus transitions nothing, so a row that has gone quiet is a job YOU
decide is possibly dead, and a reported overlap is a pair you either intended or
must repartition. It also marks a live job `orphan` when its root job has ended,
`stale` when it was not updated within `jobs.stale_after` (only when that key is
set), and `overdue`, and names `magus job exit <id>` for each; `magus doctor`
reports the first two. Ending the row stays yours.

`--timeout <duration>` on fork is OPTIONAL and unset by default. Past it the guard
denies every write graded under that lease, and its paths stop blocking other
jobs; the row stays live until you end it. A job with acceptance criteria needs no
bound.

`magus job wait` holds a writing job's `changed_paths` to the diff magus observes
since its checkpoint: every claimed path must be in it, something in it must be
inside the write paths, and a diff it cannot read FAILS. So fork writing jobs with
a checkpoint. It also refuses pass while any descendant is still live, and grades
a child against its ancestors' symbol gates as well as its own.

The store RECORDS and the agent guard GRADES. A worker that exported
`magus.lease` has each file write judged against these declarations as it
happens: inside its own write paths passes; inside its deny paths, or inside
another live job's write paths, is DENIED, and the denial names the owning
job, how long ago it was last updated, and the exit command that releases it. A writer magus cannot attribute - a person in their own checkout, or a
worker that never enrolled - is ADVISED and never blocked, and every uncertainty
fails open the same way. It is a seatbelt for harnesses that opt in, not a
sandbox. So a denied worker
COORDINATES and never works around: ask the orchestrator to re-partition, or have
the owning job release the path with the `write_paths` write above once it has
finished editing, then retry. Step 1 of Integrate
and verify checks the same boundary against the checkpoint, and that is the half
that does not depend on a worker cooperating.

A bound lease is denied one class of write regardless of its write paths: the host's own
guard wiring - `.claude/settings.json` and its hooks, `.cursor/hooks.json`,
`.codex/hooks.json`, `.opencode/plugins/`, and each host's equivalent.

Every guard verdict carries `lease`: the job it graded the write under, read from
`--lease` or the `magus.lease` baggage member. An id the store does not carry
is a DENY, not a silent pass-through. An id naming a row already in `pass`, `fail`, or
`no_return` prints one notice per session that its rules are inert.

A `next` breadcrumb magus itself served is PRE-AUTHORIZED for the call it names:
no advisory fires and the role-scoped rules stand down for that exact
command. The workspace-wide denies never yield to it - a raw language
tool, a pipe or redirect of magus's own output, a whole-tree VCS op, a relocated
checkout.
`magus session hints` reports how often a served breadcrumb was actually taken
up, per id, over the sessions this repository has loaded.

A read-only job carries an abbreviated row: no write paths, no deny paths. Every
row ends in pass, fail, or NO-RETURN, and the root writes which: silence is not a pass.

Make acceptance criteria observable: named tests,
artifacts, diagnostics, API behavior, or review checks. A child that hands work on must
evaluate its descendants before reporting upward.

## Declare the criteria magus can check for you

A job's acceptance criteria are prose a reader grades. A COMPLETION GATE is the
part magus grades itself, from evidence the worker cannot author, and `magus job
wait` refuses to record pass until every one verifies. Declare them at fork:

```sh
magus job fork api/migrate \
  --gate-check green='go-test api' \
  --gate-paths migration='db/migrations/**' \
  --gate-symbol-unreferenced unused='LegacyAccountStore'
```

A gate names WHAT it examines and what must be true of it:

| kind     | expects                                         | read from                                      |
| -------- | ----------------------------------------------- | ---------------------------------------------- |
| `check`  | `passed`                                        | a recorded run, captured after the declaration |
| `paths`  | `changed`, `present`, `absent`                  | the diff since the checkpoint, or the tree now |
| `symbol` | `changed`, `present`, `absent`, `unreferenced`  | the knowledge graph, below file granularity    |

Each kind has a default expectation, so the common gate declares only its
subject; the other flags spell it out (`--gate-paths-present`,
`--gate-symbol-absent`, ...). `magus job fork -h` lists them all.

Reach for `symbol` + `unreferenced` when partitioning a rename. It is the REMAINDER rule
made checkable: split per project and the callers in no project belong to no job,
so every job passes and the rename is unfinished.

Every kind reads what magus already holds, which is what makes a gate a contract
rather than an attestation. There is no escape hatch for "this command exited 0",
deliberately: magus did not record that run and cannot attribute it, so it would
be the easiest gate of all to satisfy falsely. Declare a target and use `check`.

The worker's own `changed_paths` is its account of its work and is never the
evidence. An observation magus could not MAKE fails the gate rather than passing
it, because the cheapest way past a gate that shrugged would be to break the
observation.

Ask where a job stands without advancing it:

```sh
magus describe job <job> --gates
```

Same grading `magus job wait` does, recording nothing, exit 1 while any gate is
unmet. Use it instead of asking a worker how it is going: the answer is graded
from evidence rather than composed by the thing being asked about.

SEQUENCE gates with `depends_on` between them, which is how one is cleared
before another is approached; a failed prerequisite propagates. Do NOT nest
them: a gate that wants children is a JOB that wants splitting, and the rule
above already covers it. Gates stay flat so the job tree stays the only
hierarchy with an owner.

A gate is not the gate. `--gate-check` still may not name `ci` or anything that
chains to it, for the reason the check rule gives above.

Run workers non-blocking by default, and block on one only when your next action
requires its result. An agent spawned merely to wait, poll, or repeat discovery the
root already owns is not an edit job and spends budget for nothing.

## Observe through the correct control plane

Use the provider's agent or task view to track the job tree, agent state,
messages, and completion. Use this to keep the store aware of descendants.

Use Magus to watch processes and shared workspace resources:

```sh
magus status --watch=15s
```

This shows Magus process state, lock holders, and shared-service
state and adoption. It does not show an agent that is thinking without running a
Magus process. Do not replace it with sleep loops, repeated `ps`, or a waiting
agent.

### "How is it going" is a READ, never a message

Never message a worker to find out how it is doing. The question costs it the
turn it was in the middle of, and what comes back is its account of itself rather
than what happened. Three reads answer it without touching the
worker.

| you want | read |
| --- | --- |
| to hand the question to a person | the console link every verb that names a job prints |
| to watch it happen | `magus job watch <job>` |
| to know whether it is finished | `magus describe job <job> --gates` |

`magus job watch` merges files changed under the job's write paths, the tool
calls the guard saw under its lease, and the runs recorded against it, one line
each until you interrupt it. A file is attributed by WRITE PATH and by nothing the
worker says, so the worker cannot make it quiet.

Message a worker only to CHANGE what it was handed. Anything you merely want to
KNOW is one of the three reads above.

A blocked worker RAISES rather than stalling quietly. Piping the block to `magus
session notify --outcome waiting` (blocked on input) or `--outcome permission` (blocked on
approval) opens a durable request in this repository; no other outcome opens one.
`magus session attention` lists what is open, and `magus session attention -q` prints nothing and exits 1 on an
empty queue, which is the form to test from a loop. Nothing closes a request by
itself: the orchestrator, or any human, disposes it with `magus session dispose
<id> --reason "<why>"`. A worker
that raised one waits for the disposition instead of choosing for itself.

`magus session` is how the root audits what a job actually RAN, as opposed
to what it reported. Each invocation carries the job it was launched under -
the same `magus.lease` channel - along with the spawner label and parent span it
claimed, the targets it finished and how they ended, and the store is keyed by
repository identity, so a worker in its own worktree is still listed here. `magus session --since 2h -o json` is the
form that answers what the fleet has been doing.

Re-plan when nesting, dependencies, ownership, failing
criteria, locks, or services change. Update the jobs before resuming affected
work, and never act on a guessed or stale PID. A running worker keeps the
constraints it was handed: tightening them means cancel and respawn, not a message
sent mid-flight.

## Integrate and verify

As jobs finish:

1. Compare the store against the ACTUAL diff since each job's checkpoint, not
   the paths it reported (`magus graph diff --rev <revision>` for the domain; a
   differing dirty digest means it saw a tree you are not diffing).
2. Run `magus job wait <job>` on each returned job BEFORE you read its result.
   It checks what is mechanical: every changed path inside the declared write
   paths and outside the denied ones, a change set that is not empty on a row that
   writes, the descendants the store carries, and the output ref
   bound to a run of THAT JOB'S OWN CHECK which the store recorded as
   PASSING. A job that verifies is recorded
   `pass`; a rejection exits 1 naming every violation, and a result that will not
   decode at all exits 2. A holder does not verify its own job, or a sibling's.
   Then reopen the
   evidence yourself (`magus query output <ref>`): wait proves the ref names a
   passing run of this job's own check, never that the work satisfies the job's
   GOAL, and a worker's own prose about its criteria is not that evidence.
3. Resolve cross-job API changes centrally; never assign the same seam twice.
4. Regenerate declared outputs once after source work converges.
5. Re-run `magus affected <target> --plan` over the actual diff. If its shape
   invalidates the original partition, stop parallel integration and reconcile.
6. Read the integrated changeset with `magus diff --impact` before landing it: what
   the fleet's combined edit reaches, who else has been changing it, an estimate of
   the rebuild from recorded run times, what the advisors say, and any note anchored
   to a file it touched. Context, never a verdict, and an empty section means nobody
   could measure it rather than nothing found.
7. Run `magus affected ci` and evaluate the top-level acceptance criteria.

Prefer fewer proven-independent jobs over
wide fan-out and conflict repair.
````


</section>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-multi-agent-full/SKILL.md
```

````markdown
# Splitting work across agents

Count the WRITE SETS your change needs - the distinct groups of files that must be
edited - not the projects a change invalidates. That distinction decides everything
here, and getting it backwards is the standard way fan-out goes wrong: a one-line
edit in a central package invalidates half the workspace and is still one edit,
and fanning it out produces several agents editing one file.

`magus affected <target> --plan` partitions VALIDATION - which targets to run,
grouped for runner balance. It is not an edit assignment and not a proof of write
isolation (the section below is what establishes that). So it can veto a fan-out
and never license one: one shard means keep the work local; several shards mean the
testing parallelizes, and whether the editing does is still your question to answer.

Before any edits exist there is no diff to plan, so the shard plan is empty and
proves nothing. Finding the candidate paths IS the partitioning work, and it is
done with the graph, not by intuition:

```sh
magus graph build                           # first in a fresh worktree, or refs answers "unknown, not absent"
magus refs <symbol> --occurrences -o json   # every edit site, column-precise
magus explain <node>                        # one node's edges and blast radius
magus affected ci --plan --stdin            # plan PROPOSED paths, before editing
```

You do not need to be asked. "Split this across agents" is one way in;
several disjoint write sets you can name is another.

Fan-out is not inherently expensive. What costs is unbounded fan-out:
workers that hand work on without a shrinking scope, jobs with no acceptance criteria
so nobody can say when to stop, and a principal model assigned to mechanical edits.
Each of those is a choice made below, not a property of fanning out. Say what a
round will cost when the user is deciding, and prefer the smallest fan-out that
covers the work.

Fan out only after the collision check below REPORTS the jobs disjoint; write
sets that look separate is not that check. If the graph supports only one
coherent write set, keep the work local - fanning out one job adds coordination
and buys nothing. The root agent owns the goal, the budget, the topology,
integration, and final verification, and never hands those out.

## Coalesce what the write sets allow

Disjoint write sets LICENSE parallelism; they do not require it. Every worker
carries a fixed load before it reads a line of the diff - a system
prompt, the repository's instruction file, the routing index, the skills that
load, and the graph queries it runs to find its own footing - spent identically
whether the unit is fifty lines or five hundred. So the question after
partitioning is never "may these run in parallel" but "is each one big enough to
be worth a worker".

Four rules, in the order they bite:

- A depends-on chain is ONE worker in sequence, not two workers in turn.
  Two is two loads for one unit of work, and the second starts by rediscovering
  what the first just learned.
- Merge small disjoint units inside one project. The wall-clock gain from
  splitting them is usually smaller than the load you pay twice, and
  they contend on the same validation anyway.
- Spawn only when more than one independent BIG unit survives the merge. One unit
  is inline work: a brief longer than the diff it asks for is the
  tell.
- Fill idle root time from that pool, never by splitting finer. A root blocked on
  a gate is a reason to start the next merged unit, and cutting a unit
  in half to have something to spawn buys wall clock with two fixed loads.

## Run the graph-engineering loop

Graph engineering is a natural evolution of loop engineering. The
human supplies a goal and constraints; the root agent turns them into explicit
acceptance criteria, uses the knowledge graph to partition the work, hands out
bounded prompts, observes results, evaluates them against the criteria, and
course-corrects until the integrated goal is satisfied. The graph improves the
loop's partition and collision decisions; it does not replace the loop.

Run this control loop:

1. State the top-level goal, constraints, and observable acceptance criteria.
2. Map the affected graph and propose collision-resistant edit jobs.
3. Give every job its own criteria, ownership boundary, and completion gates.
4. Hand out work, within any cap the user set.
5. Observe agents and Magus processes through their separate control planes.
6. Evaluate evidence, revise ownership or ordering when assumptions change, and
   repeat until the criteria pass.
7. Integrate centrally and run the release gate.

Acceptance evidence is an output ref the root reopens (`magus query output <ref>`),
never a worker's prose. A worker that ran a filtered subset, or quietly restated its
criteria into something it did pass, reports success either way - and a
transcript cannot tell you which happened.

An agent may report that its edits are done, but no job is complete until its
acceptance criteria and assigned check pass. The root agent, not a worker,
decides whether the top-level goal is complete.

## Declare the interface before any job forks

When a change adds a shared surface - a module several call sites will use, a type,
an event, an exported function - the ROOT names it before any edit: the path, every
exported name with its signature, and the EXISTING symbols it must reuse rather than
restate. Workers implement names they were handed and never coin a public one. A
name invented at each call site is how one concept ends up with five spellings, and how a plan that says "collapse two state types into one" ships
a third: each site made a locally sensible choice and nobody owned the set.

Then make the declaration gradeable, so the names are a contract rather than a
suggestion:

- `symbol` + `present` for each new exported name.
- `symbol` + `absent` for the name a second copy would predictably take beside a
  symbol the work must reuse. No gate grades reuse itself: `present`
  holds for a symbol that existed before the job began, and a reference count
  cannot tell the defining file or an import from real use.
- `symbol` + `unreferenced` for each helper the shared surface replaces.

Check each declared name with `magus refs` before forking. A bare name that
resolves to more than one definition is graded against one of them, silently.

This applies with zero workers. A solo change that crosses several call sites is a
one-job tree, and skipping the declaration because nothing is being forked is the
same failure without a fork to blame. Write the table in the
conversation, check it with `magus refs` when the edits land, and only then report
the change done.

## Set one topology boundary

Before spawning, state the topology: the model per job and whether isolated
worktrees are available. Fan-out and depth are not capped unless the workspace
sets a cap: spawn as many jobs, nested as deep, as the partition supports. A limit
exists only when magus.yaml's `jobs` section sets one (read it with
`magus config view` or `magus_config_get`): `max_depth` and `max_live`
make `magus job fork` refuse past them, naming the key, and
`default_timeout` bounds a fork that names no `--timeout`. Honor a cap the user
states the same way. Editing costs the workspace nothing; what contends is VALIDATION - the
magus runs a job triggers - so read the live pool (`magus status`) before
starting a validation rather than before starting an agent, and serialize
validations that share a worktree even when their write sets are disjoint.

Assign the check from the pipeline the workspace composed, not from convention:
`magus describe target ci <project>` names what `ci` chains and in what order,
so a job gets the narrowest target from that decomposition and the integrator
re-runs the described order, with `magus affected ci` re-proving the whole
composition. A worker hand-sequencing lint, format, and test is re-deriving an
order the magusfile already owns, and the step it forgets fails silently by
omission.

A job's check is that narrow target and never the gate; the guard reads
the row and denies `ci` to any worker whose check names something else. The
root gates ONCE, in its own tree, after every unit lands, which is
what stops seven fanned-out workers from each running the whole pipeline
concurrently on one machine.

Decide each job's validation PLANE with its target. A worker environment that
cannot execute magus at all - an isolated tree with no usable binary, or a
guard that routes raw language tools back to targets it cannot run - cannot
validate anything it writes. Mark that job's check ROOT-DEFERRED when you fork
it: the worker writes the tests, stops at the static checks
its environment does run, and says so; the root executes the job's target
centrally before verifying. Leaving each worker to discover the wall
spends its budget on the discovery, once per worker, and its report then reads
"done" with nothing executed - which the acceptance-evidence rule above already
refuses to accept.

A worker may fork part of its own job. What it may not do is hand it out
without shrinking the problem - that is the shape that does not terminate, and
the cost people attribute to "multi-agent" is almost always this. These rules give
it a definitive end without capping how deep it goes:

- **Every level narrows.** A child's scope is a strict subset of its parent's. A
  worker that would hand on its whole job should do the work instead.
- **Every job carries acceptance criteria down with it.** A child inherits its
  parent's criteria plus its own. A job nobody can evaluate is a job that cannot
  end, which is what makes depth dangerous rather than the nesting itself.
- **A job that fails its criteria twice is not re-issued.** The root does it
  locally, or serializes it behind whatever keeps breaking it. Two jobs
  with an undeclared dependency each break the other's criteria, and re-issuing
  the failing one satisfies every rule above while alternating forever; the budget
  is what ends it.
- **Whatever the parent does not hand out, the parent still owns.** A strict subset
  leaks by construction: split "no caller of X remains" into per-project jobs and
  the callers in no project belong to nobody, so every job passes and the goal is
  unmet. Carry a remainder row at each level and close it explicitly.

Pick the model that FITS the job, and SAY which one. That is the whole rule, and it
runs both ways: a mechanical rename does not need the strongest model available, and
an ambiguous API boundary does not get the cheapest one because it looked like less
work. Matching the model to the work is the only cost decision worth making here -
past that, cost is not your call to agonize over, and a job done badly by an
under-powered worker costs more than the model it saved.

Naming it is the half that is checkable. Every spawn names a model, or names an agent
definition that names one. Inheriting the parent's model ON PURPOSE is fine and often
right, since a hard review under a cheaper coordinator is exactly the case an ordering
rule would forbid. Inheriting it BY OMISSION is the failure: a host whose default is
"same as the parent" turns every unnamed spawn into the most expensive one available,
and nothing afterwards records that no choice was made. Measured 2026-09-15: nine
workers spawned in one session, every one inheriting the root's model, five of them
mechanical work a cheaper model does as well.

ASK THE HUMAN when the right model is unclear, before spawning rather than after the
budget is spent. There is no ordering rule to fall back on: "only ever spawn something
weaker" was tried and withdrawn, because same-strength offload is legitimate. So an
unclear case is a question, not a default.

Model NAMES belong to the host, never to magus: they change faster than any table here
could track. Name the model your host names, or point the spawn at an agent definition
the user owns. Where the host has a default-subagent setting, setting it is what makes
omission cheap instead of expensive.

Map work to provider capabilities without assuming model names:

| Model | Assign |
|---|---|
| principal | architecture, ambiguous ownership, public APIs, migrations, security, integration |
| standard | isolated implementation with a clear contract and bounded project surface |
| economy | mechanical edits, fixtures, docs, inventory, and read-only evidence gathering |

If the host cannot select models or reasoning effort, keep its default. Tool surface
is a separate axis from the model: evidence gathering, scouting, and review get a
read-only tool surface where the host offers one. Never downgrade the root
integration pass or final release gate.

Nesting is allowed when the host supports it, but it does not create a
new budget or a private ownership map. Before a child spawns descendants, it must
report the proposed jobs to its parent. The store must then carry those
descendants, their parent, model, criteria, and write paths. Descendants
inherit the ancestor's deny paths and may subdivide only the ancestor's
write paths. A cap the user sets applies to the whole tree, not once per parent.

Keep one integration owner at the root even when the job tree is deep.
A child may coordinate its descendants, but it may not accept changes
outside its own job, relax top-level acceptance criteria, or hide additional
fan-out from the root. Nest wherever a child has a genuinely separable area and
enough context to partition it better than its parent.

## Seed the partition with Magus

Choose the target that will validate the work. `ci` is the release gate, but any
target accepted by `magus affected <target>` can be planned:

```sh
magus affected <target> --plan
```

For a proposed change whose paths are known but not edited yet, plan those paths
instead of the current diff:

```sh
printf '%s\n' <repo-relative-path>... | magus affected <target> --stdin --plan
```

Read the JSON fields `count`, `max_parallel`, `source`, and `matrix`. A shard is a
history-balanced execution group, not an edit assignment or proof of write
isolation. Use it only as the first partition. If paths are not known, query the
task's symbols, files, and projects before producing a stdin plan.

## Prove that jobs do not collide

Classify the union of every job's proposed paths in one call:

```sh
magus describe file <both jobs' paths>... -o json
```

Read the facts: `overlaps` lists each declaration covering more than one
proposed path - a shared write set by construction; `claims[].target` names the
target that regenerates a path (generated outputs have one integration owner,
never hand-edited by workers); `depends_on` carries the owner's direct edges.
Affinity stays with `magus_insight lens=affinity`, and `magus refs <symbol>`
when two jobs may touch the same API; `magus path <a> <b>` settles
a suspicious pair. A read-only job has no write set, so it is outside
this analysis entirely.

Two jobs may run together only when:

- The combined classification reports no overlaps - source write sets and
  declared outputs disjoint.
- Neither consumes an API or generated artifact the other will change.
- Shared manifests, lockfiles, schemas, workspace configuration, and agent
  instructions have one owner.
- Dependency and temporal-affinity evidence does not indicate that they should
  move together.

ONE WORKTREE PER WORKER wherever a write path touches workspace configuration - any
project's magusfile, its `magus.yaml`, or a spell source a magusfile imports.
Those are the files magus READS to load the workspace, so a half-saved one is
not a conflict between two workers: it stops the workspace loading for everybody
in that checkout at once, and the rest lose `magus run`, `magus ls` and their own
tests over an edit they cannot see. Measured 2026-09-17: three workers
shared a checkout, one saved the root magusfile mid-edit, and all three were
stopped for the duration by a file only one of them had ever opened.
`magus job fork` refuses such a job outright while another live job with
write paths is bound to the same checkout, naming the file and the holder. A
separate worktree is the answer, not a narrower boundary: the file has one owner, so
the only way both jobs can be right is for one of them to be somewhere else.

Fork records what it could prove in `write_proof` - `alone`, `disjoint` or
`overlapping` - and `magus ls jobs` prints it. An overlap is recorded rather than
refused, because sequencing two jobs onto one path is a call only you can make;
what the row settles is whether anybody checked.

Spawning into a checkout that already holds a live job carries the same
reminder from the guard, once per session, with the union of the write paths to
classify. Answer it by running the call above or by handing the new worker its
own worktree - a proof or a worktree, not a judgment that it looks fine.

Project boundaries alone are insufficient. A declared dependency means group the
work or serialize producer before consumer. Treat strong hidden affinity as a
warning. When evidence is incomplete, reduce parallelism.

## Fork one job per unit of work

Before spawning, fork one job per unit - including the checkpoint it was handed
(`magus vcs checkpoint -o name`: the revision, plus a dirty-patch digest when the
tree is not clean) - and keep descendants in the same store. Fork each one with
the `magus_job` tool from the orchestrating agent, or `magus job fork` from a
person at a terminal - the same store and the same
authorization rule either way, so a job forked by hand and one an agent forked are
indistinguishable to everything that reads them:

The same checkpoint is what a later incremental re-review diffs from (see the
magus-change-summary skill) - review time and pickup time read the same object.

| Job | Parent | Checkpoint | Criteria | Completion gates | Write paths | Deny paths | Read paths | Depends on | Model | Check | State |
|---|---|---|---|---|---|---|---|---|---|---|

Render the prompt FROM the row rather than typing it: `magus describe job <job>`
prints the job's own criteria, boundary and check, plus what the workspace knows
and nobody wrote down - the projects the write paths reach, the declared
output globs that land inside them, the paths a sibling job is holding, the build
inputs and workspace configuration that have one owner, and the projects that
change alongside the leased ones without declaring a dependency. Two renders
of one row are byte-identical, which hand-typed prompts are not: seven of
them disagreed about a dedup key and every one ended with the gate. It
REFUSES a job whose check is the gate, or a target that chains to one.

Every worker prompt must include its row, its JOB ID, relevant graph
evidence, and the global spawn rule. Require the worker to export
`BAGGAGE=magus.lease=<its id>` before it works - that is the W3C
Baggage channel, and the member is what tells the agent guard whose declared boundary
to grade a write against, so a worker that never exports it is graded as an editor
magus cannot attribute. Export `TRACEPARENT` too when your host has one, and add
`magus.spawner=<your label>` to the baggage: magus records the trace, the parent span
and the label as CLAIMS, so `magus session ls` can show who spawned whom, and no
verdict is ever keyed on them. Require it to preserve
unrelated changes, stay inside write paths, avoid generated outputs, run only its
assigned Magus target, and return its result with `magus job exit --stdin` in the
schema `magus job exit --schema` prints: changed paths, the validation it ran with
the output ref that proves it, descendants it forked, and unresolved risks. A typed
result is what makes verification mechanical; the same four facts in prose can only be
graded by reading, and a worker that ran a filtered subset writes the same
paragraph as one that did not. Keep unresolved risks mandatory, so a
mis-scoped worker can say so instead of widening silently.

Taking a lease narrows what a worker may write in the store, never what it may see. Its
own row accepts four writes: recording the base it landed on, shrinking its own
write_paths (how it releases a path), ending itself in fail or no_return, and
forking a child inside its own write paths. Everything else, including any other field
on its own row and any write to a row that is not its own or its child, is the
orchestrator's alone, and the store refuses the rest before a worker
gets far enough to try it a second way: the refusal names the actor directly -
your orchestrator writes what a worker may not; report it as an unresolved risk
and stop.

The checkpoint you recorded is what you HANDED the job; the base it
actually LANDED ON is a separate fact, because hosts that isolate workers in
per-worker trees routinely branch them from an older revision than the tree you
partitioned - and every diff-since-checkpoint in Integrate and verify
silently lies when the recorded base is not the real one. A worker's first
required act is `magus job exec <its id>`, which takes the lease in that checkout
and records the base it landed on. Where the host names its session, the worker
passes `--session <that id>` and the lease is the SESSION's rather than the
checkout's, so workers sharing a tree each hold one and each has its own write
paths graded; without it the binding is the whole checkout's, the second
worker's is refused as a rebind, and every worker after the first runs
unattributed, which looks exactly like a guarded session and denies nothing.
Pass the same id the host reports to its guard hook, or the two halves bind and
grade under different names. The
answer is a status recorded on the row - match, revision-match (same revision,
different uncommitted patch), diverged, or unknown - plus a reading of it that
names both tokens and the next step. It is a FACT and not a gate: every status
records, diverged included, because refusing would leave the orchestrator
with no record that a worker went to the wrong base, which is the one case the
record exists for. Acting on it is still yours: respawn from the right
revision, or have the worker materialize the files it builds on from the intended
one (`git show <rev>:<path> > <path>`, verifying each blob against
`git rev-parse <rev>:<path>`) and re-fork the job with the right checkpoint. A worker
that edits stale content without noticing reports clean validation against a tree
nobody will ever merge.
Also name any fact that will READ as drift to the worker's snapshot - a project
deleted this session, a rename, an index regenerated underneath it - never a
generic "expect drift" line, which only primes the worker to dismiss real
anomalies: the specific fact is what keeps unexplained tree state from costing
an investigation or a helpful revert of something correct.

Write paths are the WRITE boundary. The guard reads them as a READ boundary too, so a
worker leased to `apps/web` is advised off `apps/admin` and denied it outright
once `magus job exec <its id>` takes the lease in its checkout. The
read boundary is its projects plus what they declare `depends_on`, so a shared library
it legitimately builds on stays open. When a worker must READ something it
must not WRITE, put that path in the row's `read_paths` instead of widening
`write_paths`: one list cannot say both, and widening the write paths to open a
read is how two workers end up owning one file. `read_paths` is the only widening
there is; nothing in the environment turns the rule off.

Ownership ends when EDITING ends, not when the worker exits. A worker that has
finished writing a contested path announces the release immediately - shrink the
job's `write_paths` with another `magus_job` write, or message the orchestrator
if the host supports it - and then carries on validating. A waiting job
starts against the released file while the first is still running tests, which
is most of a worker's lifetime; holding every path to exit serializes agents on
time they spend not editing.

That write records each dropped path with the digest it carried at that moment.
Hand the digest to the job taking the path over: it names the version being
inherited, and one that no longer matches at verification means the waiter built
on a tree the releaser never saw.

Advance the row on every state change. `magus ls jobs` then answers two questions you
would otherwise derive by hand: which live jobs claim intersecting
`write_paths`, and how long since each row was touched. Both are facts, not
verdicts - magus transitions nothing, so a row that has gone quiet is a job YOU
decide is possibly dead, and a reported overlap is a pair you either intended or
must repartition. It also marks a live job `orphan` when its root job has ended,
`stale` when it was not updated within `jobs.stale_after` (only when that key is
set), and `overdue`, and names `magus job exit <id>` for each; `magus doctor`
reports the first two. Ending the row stays yours.

`--timeout <duration>` on fork is OPTIONAL and unset by default. Past it the guard
denies every write graded under that lease, and its paths stop blocking other
jobs; the row stays live until you end it. A job with acceptance criteria needs no
bound: the gates end it, and a timeout only helps where a stuck worker
would otherwise hold paths nobody else can write.

`magus job wait` holds a writing job's `changed_paths` to the diff magus observes
since its checkpoint: every claimed path must be in it, something in it must be
inside the write paths, and a diff it cannot read FAILS. So fork writing jobs with
a checkpoint. It also refuses pass while any descendant is still live, and grades
a child against its ancestors' symbol gates as well as its own.

The store RECORDS and the agent guard GRADES. A worker that exported
`magus.lease` has each file write judged against these declarations as it
happens: inside its own write paths passes; inside its deny paths, or inside
another live job's write paths, is DENIED, and the denial names the owning
job, how long ago it was last updated, and the exit command that releases it. A writer magus cannot attribute - a person in their own checkout, or a
worker that never enrolled - is ADVISED and never blocked, and every uncertainty
fails open the same way: no jobs declared, none live, a store
that will not parse. It is a seatbelt for harnesses that opt in, not a
sandbox. So a denied worker
COORDINATES and never works around: ask the orchestrator to re-partition, or have
the owning job release the path with the `write_paths` write above once it has
finished editing, then retry. Editing anyway from an un-enrolled shell, or
dropping the lease id to buy advisory treatment, turns a denial you could have
acted on into a collision nobody sees until integration. Step 1 of Integrate
and verify checks the same boundary against the checkpoint, and that is the half
that does not depend on a worker cooperating.

A bound lease is denied one class of write regardless of its write paths: the host's own
guard wiring - `.claude/settings.json` and its hooks, `.cursor/hooks.json`,
`.codex/hooks.json`, `.opencode/plugins/`, and each host's equivalent.
Those files switch the guard on for the host's next session start, so an edit
inside them is never a write-path question. An unbound session gets a once-per-session
advisory instead, because rewiring the host is ordinarily the orchestrator's or a
person's job.

Every guard verdict carries `lease`: the job it graded the write under, read from
`--lease` or the `magus.lease` baggage member. An id the store does not carry
is a DENY, not a silent pass-through - every lease-scoped rule reads
that row, so a typo'd id would otherwise be graded by nothing while the worker
believed itself bounded. An id naming a row already in `pass`, `fail`, or
`no_return` prints one notice per session that its rules are inert,
because those rules only ever read live rows.

A `next` breadcrumb magus itself served is PRE-AUTHORIZED for the call it names:
no advisory fires and the role-scoped rules stand down for that exact
command, matched argv for argv with only the binary's own spelling
normalized. The workspace-wide denies never yield to it - a raw language
tool, a pipe or redirect of magus's own output, a whole-tree VCS op, a relocated
checkout, because those protect everyone rather than one role.
`magus session hints` reports how often a served breadcrumb was actually taken
up, per id, over the sessions this repository has loaded: served,
followed, rejected and reflex-repeated counts, and the rate below which a hint is
spending context on advice nobody takes.

A read-only job carries an abbreviated row: no write paths, no deny paths. Every
row ends in pass, fail, or NO-RETURN, and the root writes which: silence
is not a pass, and a worker that dies, stalls, or is killed is a different state
from one that failed its criteria.

Acceptance criteria must be observable. Prefer named tests, generated
artifacts, diagnostics, API behavior, or specific review checks over phrases such
as "works correctly." A child that hands work on remains responsible for evaluating
its descendants before reporting upward. The root still verifies the combined
result independently.

## Declare the criteria magus can check for you

A job's acceptance criteria are prose a reader grades. A COMPLETION GATE is the
part magus grades itself, from evidence the worker cannot author, and `magus job
wait` refuses to record pass until every one verifies. Declare them at fork:

```sh
magus job fork api/migrate \
  --gate-check green='go-test api' \
  --gate-paths migration='db/migrations/**' \
  --gate-symbol-unreferenced unused='LegacyAccountStore'
```

A gate names WHAT it examines and what must be true of it:

| kind     | expects                                         | read from                                      |
| -------- | ----------------------------------------------- | ---------------------------------------------- |
| `check`  | `passed`                                        | a recorded run, captured after the declaration |
| `paths`  | `changed`, `present`, `absent`                  | the diff since the checkpoint, or the tree now |
| `symbol` | `changed`, `present`, `absent`, `unreferenced`  | the knowledge graph, below file granularity    |

Each kind has a default expectation, so the common gate declares only its
subject; the other flags spell it out (`--gate-paths-present`,
`--gate-symbol-absent`, ...). `magus job fork -h` lists them all.

Reach for `symbol` + `unreferenced` when partitioning a rename. It is the REMAINDER rule
made checkable: split per project and the callers in no project belong to no job,
so every job passes and the rename is unfinished.

Every kind reads what magus already holds, which is what makes a gate a contract
rather than an attestation. There is no escape hatch for "this command exited 0",
deliberately: magus did not record that run and cannot attribute it, so it would
be the easiest gate of all to satisfy falsely. Declare a target and use `check`.

The worker's own `changed_paths` is its account of its work and is never the
evidence. An observation magus could not MAKE fails the gate rather than passing
it, because the cheapest way past a gate that shrugged would be to break the
observation.

Ask where a job stands without advancing it:

```sh
magus describe job <job> --gates
```

Same grading `magus job wait` does, recording nothing, exit 1 while any gate is
unmet. Use it instead of asking a worker how it is going: the answer is graded
from evidence rather than composed by the thing being asked about.

SEQUENCE gates with `depends_on` between them, which is how one is cleared
before another is approached; a failed prerequisite propagates. Do NOT nest
them: a gate that wants children is a JOB that wants splitting, and the rule
above already covers it. Gates stay flat so the job tree stays the only
hierarchy with an owner.

A gate is not the gate. `--gate-check` still may not name `ci` or anything that
chains to it, for the reason the check rule gives above.

Run workers non-blocking by default, and block on one only when your next action
requires its result. An agent spawned merely to wait, poll, or repeat discovery the
root already owns is not an edit job and spends budget for nothing.

## Observe through the correct control plane

Use the provider's agent or task view to track the job tree, agent state,
messages, and completion. Use this to keep the store aware of descendants.

Use Magus to watch processes and shared workspace resources:

```sh
magus status --watch=15s
```

This shows Magus process state, lock holders, and shared-service
state and adoption. It does not show an agent that is thinking without running a
Magus process. Do not replace it with sleep loops, repeated `ps`, or a waiting
agent.

### "How is it going" is a READ, never a message

Never message a worker to find out how it is doing. The question costs it the
turn it was in the middle of, and what comes back is its account of itself rather
than what happened. Three reads answer it and none of them needs the
worker's cooperation: a changed file is the filesystem reporting a fact, a tool
call is what the guard already recorded, and a gate is graded against evidence
magus is holding anyway.

| you want | read |
| --- | --- |
| to hand the question to a person | the console link every verb that names a job prints |
| to watch it happen | `magus job watch <job>` |
| to know whether it is finished | `magus describe job <job> --gates` |

`magus job watch` merges files changed under the job's write paths, the tool
calls the guard saw under its lease, and the runs recorded against it, one line
each until you interrupt it. A file is attributed by WRITE PATH and by nothing the
worker says: the write paths are proven disjoint when the job forks, so the
path alone names the holder. Where two live jobs do cover one path the line says
`contested`, names both, and attributes it to neither - there is nothing in a path
to break that tie with, and naming one would tell you a file moved under a worker
that never touched it.

Message a worker only to CHANGE what it was handed. Anything you merely want to
KNOW is one of the three reads above.

A blocked worker RAISES rather than stalling quietly. Piping the block to `magus
session notify --outcome waiting` (blocked on input) or `--outcome permission` (blocked on
approval) opens a durable request in this repository; no other outcome opens one.
`magus session attention` lists what is open, keyed by repository identity rather
than by checkout path, so a request raised inside a worker's own isolated tree is
listed in yours, and `magus session attention -q` prints nothing and exits 1 on an
empty queue, which is the form to test from a loop. Nothing closes a request by
itself: the orchestrator, or any human, disposes it with `magus session dispose
<id> --reason "<why>"`. There is no expiry and no auto-dispose, because a
request magus could answer on its own would not have needed a person. A worker
that raised one waits for the disposition instead of choosing for itself.

`magus session` is how the root audits what a job actually RAN, as opposed
to what it reported. Each invocation carries the job it was launched under -
the same `magus.lease` channel - along with the spawner label and parent span it
claimed, the targets it finished and how they ended, and the store is keyed by
repository identity, so a worker in its own worktree is still listed here.
Attribution is cooperative and every one of those values is a CLAIM magus records
rather than corroborates: an empty lease means the invocation claimed none, which
makes it unattributed, never an error; its OS user says whose account ran it. `magus session --since 2h -o json` is the
form that answers what the fleet has been doing.

Course-correct at explicit checkpoints: after a child proposes new
descendants, when a worker discovers a new API or generated-output dependency,
when ownership drifts, when criteria repeatedly fail, and when status shows
unexpected lock contention or service failure. Pause only the affected branch,
update the jobs and ordering, then resume work that remains independent. Never
guess a PID or signal from stale output; use current status and the host's normal
process controls. A running worker keeps the
constraints it was handed: tightening them means cancel and respawn, not a message
sent mid-flight.

## Integrate and verify

As jobs finish:

1. Compare the store against the ACTUAL diff since each job's checkpoint, not
   the paths it reported (`magus graph diff --rev <revision>` for the domain; a
   differing dirty digest means it saw a tree you are not diffing).
2. Run `magus job wait <job>` on each returned job BEFORE you read its result.
   It checks what is mechanical: every changed path inside the declared write
   paths and outside the denied ones, a change set that is not empty on a row that
   writes, the descendants the store carries, and the output ref
   bound to a run of THAT JOB'S OWN CHECK which the store recorded as
   PASSING. There is no `passed` field on the result: wait reads the
   ref's own recorded attempt from the output store and derives the outcome from
   it, never from what the worker claims. A job that verifies is recorded
   `pass`; a rejection exits 1 naming every violation, and a result that will not
   decode at all exits 2. A holder does not verify its own job, or a sibling's.
   Then reopen the
   evidence yourself (`magus query output <ref>`): wait proves the ref names a
   passing run of this job's own check, never that the work satisfies the job's
   GOAL, and a worker's own prose about its criteria is not that evidence.
3. Resolve cross-job API changes centrally; never assign the same seam twice.
4. Regenerate declared outputs once after source work converges.
5. Re-run `magus affected <target> --plan` over the actual diff. If its shape
   invalidates the original partition, stop parallel integration and reconcile.
6. Read the integrated changeset with `magus diff --impact` before landing it: what
   the fleet's combined edit reaches, who else has been changing it, an estimate of
   the rebuild from recorded run times, what the advisors say, and any note anchored
   to a file it touched. Context, never a verdict - nothing gates on it and
   the exit code is unchanged - and a section that is empty means nobody could
   measure it, not that nothing was found.
7. Run `magus affected ci` and evaluate the top-level acceptance criteria.

Parallelism is an optimization, not the objective. Fewer
well-isolated jobs are usually cheaper than wide fan-out followed by conflict
repair, and the graph is evidence for that judgment rather than permission to
spawn every possible worker.
````


</section>

</article>
