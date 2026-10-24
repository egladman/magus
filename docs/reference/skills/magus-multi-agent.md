---
title: magus-multi-agent
generated_from: internal/agent/skills/magus-multi-agent/SKILL.md
description: "Load BEFORE your first subagent spawn in a magus workspace: an Agent or Task tool call, a background worker, parallel workers, fanning out, or delegating part of a task."
tags: [agents, skills, magus-multi-agent]
skill_full_bytes: 28719
skill_short_bytes: 21672
---

# magus-multi-agent

Load BEFORE your first subagent spawn in a magus workspace: an Agent or Task tool call, a background worker, parallel workers, fanning out, or delegating part of a task. Covers forking the job row each worker is bound to (magus job fork with FILE or glob write paths, a check, criteria and a model), the spawn description <parent>/<role> <job>, partitioning by WRITE SET from graph evidence (magus refs --occurrences, explain, affected --plan --stdin), proving the jobs cannot collide, and matching each job's model to its work. Do NOT fan out one coherent edit just because it invalidates many projects: a shard plan partitions VALIDATION, not editing.

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
| `agent-skill-version` | `147` |
| `knowledge-schema-version` | `24` |
| `skill-content` | `cad3b3f0dced` |
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

Count the WRITE SETS your change needs (the distinct groups of files to edit), not the
projects it invalidates. Getting this backwards is the standard fan-out failure. A
one-line edit in a central package invalidates half the workspace and is still one
edit.

`magus affected <target> --plan` partitions VALIDATION: which targets to run, grouped
for runner balance. It is neither an edit assignment nor a proof of write isolation.
So it can veto a fan-out and never license one. One shard means keep the work local.

Before any edits exist, the shard plan is empty and proves nothing. Finding the
candidate paths IS the partitioning work, done with the graph:

```sh
magus graph build                           # first in a fresh worktree, or refs answers "unknown, not absent"
magus refs <symbol> --occurrences -o json   # every edit site, column-precise
magus explain <node>                        # one node's edges and blast radius
magus affected ci --plan --stdin            # plan PROPOSED paths, before editing
```

You do not need to be asked to split work: several disjoint write sets
you can name are reason enough.

Say what a round
costs when the user is deciding, and prefer the smallest fan-out that covers the work.

Fan out only after the collision check below REPORTS the jobs disjoint; looking
separate is not that check. With only one coherent write set, keep the work local.

The root agent owns the goal, the budget, the topology, integration and final
verification, and never hands those out. Above the root is the human in the loop. What
lands, what is published and what a round may spend are the person's calls: the root
reports to them and does not decide them.

While a row it handed out is live, the root does no groundwork: no reading, searching or editing
of source, no runs of its own. It plans from the graph and moves jobs; a scout and integration are jobs.
The guard enforces this by role, and the role is the live lease: the root may take ONE row with
`magus job exec <row>` until `magus job exit <row>` ([the root's role](reference.md#the-roots-role)).

## Coalesce what the write sets allow

Disjoint write sets LICENSE parallelism; they do not require it. Every worker carries
a fixed load before it reads a line of the diff.

- A depends-on chain is ONE worker in sequence, not two workers in turn.
- For the rest, read the grouping lens (`magus\insight` through `client`,
  or `grouping history` in `magus describe job <job>`) and weigh each measured shape
  against the others. It reports
  and never decides.
- Where the lens answers unknown, apply these defaults and say in the plan that you
  did. Merge small disjoint units inside one project. Spawn only when more than one
  independent unit survives the merge. Fill idle root time from that pool, never by
  splitting finer.

## Run the graph-engineering loop

Prove it before you plan it. A claim a plan or brief rests on cites the output ref of
a run that settled it. A guess nobody ran stays out of the plan.

The loop: define, partition, hand out, observe, evaluate, course-correct, integrate.

- Acceptance evidence is an output ref the root reopens (`magus query output <ref>`),
  never a worker's prose.
- A worker is not complete until its criteria and assigned check pass.
- The root agent decides whether the top-level goal is complete.

## Declare the interface before any job forks

When a change adds a shared API, the ROOT names it before any edit. A shared
API is a module several call sites use, a type, an event, or an exported
function. Name its path, every exported name with its signature, and the EXISTING
symbols it must reuse, not restate.

Workers implement the names they were handed and never coin a public one. A name
invented at each call site is how one concept ends up with five spellings.

Then make the declaration gradeable:

- `symbol` + `present` for each new exported name.
- `symbol` + `absent` for the name a second copy would predictably take beside a
  symbol the work must reuse. No gate grades reuse itself.
- `symbol` + `unreferenced` for each helper the shared API replaces.

A job that adds or changes an exported API gets a `symbol` + `signature` goal by
default. A stub job lands the stub, and the root runs `magus graph build`. The root
then forks the `signature` goal empty. The fork records the indexed stub as the
contract, beside a `changed` goal on the same name.

A job's `breaks` list names the flags, JSON keys, exported symbols or stored formats
you approved it breaking. A worker that finds a break outside it stops and reports.
Each unreferenced symbol `magus job fork` lists in the write paths
(`deletion_candidates`) or `magus job wait` reports is finished or deleted. Confirm
first.

Check each declared name with `magus refs` before forking.

This applies with zero workers. A solo change across several call sites is a one-job
tree. Skipping the declaration because nothing is forked is the same failure.

## Set one topology boundary

Before spawning, state the topology: the model per job, and whether isolated worktrees are available.

- Fan-out and depth are not capped unless the workspace sets a cap. Spawn as many
  jobs, nested as deep, as the partition supports.
- A limit exists only when magus.yaml's `jobs` section sets one. Read it with
  `magus config view` or `config`.
  - `max_depth` and `max_live` make `magus job fork` refuse past them, naming the key.
  - `default_timeout` bounds a fork that names no `--timeout`.
  - Honor a cap the user states the same way.
- Editing costs the workspace nothing; VALIDATION contends: the magus runs a job
  triggers, which share the machine's build budget. The root waits for that budget,
  never a worker; a spawn or `magus job fork` into a full one says how full it is.
  Serialize validations that share a worktree, even when their write sets are disjoint.

Assign the check from the pipeline the workspace composed, not from convention.
`magus describe target ci <project>` names what `ci` chains and in what order.

- A job gets the narrowest check that covers its edits: the target from that
  decomposition. Scope it to the job's own tests with the runner's filter after `--`
  where one exists. Never the full suite.
- The integrator re-runs the described order, and `magus affected ci` re-proves the
  whole composition.

A job's check is that narrow target, never the gate. The root gates ONCE, in its own
tree, after every unit lands.

Decide each job's validation PLANE with its target. Some worker environments cannot
execute magus at all. Such a worker cannot validate what it
writes. Mark that job's check ROOT-DEFERRED when you fork it:

- the worker writes the tests, stops at the static checks its environment does run,
  and says so;
- the root executes the job's target centrally before verifying.

A worker may fork part of its own job. It may not hand it out without shrinking the
problem. These instructions give it a
definitive end without capping depth:

- **Every level narrows.** A child's scope is a strict subset of its parent's. A
  worker that would hand on its whole job does the work instead.
- **Every job carries acceptance criteria down.** A child inherits its parent's
  criteria plus its own.
- **A retry is `magus job fork <same id>`, never a new id.** The next holder reads the previous
  attempt in `magus describe job`, never a transcript. One marked not retryable names
  what changes first (`magus job apply` to widen, a config edit, or you). Make it, or
  do the job locally; never re-fork it unchanged.

  A goal unmet twice marks it so: the root re-scopes the job, or serializes it behind
  whatever keeps breaking it.
- **Whatever the parent does not hand out, the parent still owns.** A strict subset
  leaks by construction. Carry a remainder row at each level and close it explicitly.

START EVERY SPAWN AT THE ECONOMY TIER, and SAY which model you picked. Move a job up
only for work the table below assigns higher. The fit still runs both ways: an
ambiguous API boundary does not get the cheapest model because it looked like less
work.

Naming it is the checkable half: every spawn names a model, or an agent definition
that names one.

- Inheriting the parent's model ON PURPOSE is fine and often right.
- Inheriting it BY OMISSION is the failure. A host defaulting to "same as the parent"
  makes every unnamed spawn the most expensive one, and records no choice.

When the tier is unclear, start at economy. A job that fails its criteria there is
re-forked one tier up, not retried at the same one.

Tiers are the workspace's. `--model` takes a tier or a
model. magus never picks a model live: re-fork to change a tier.

| Shipped tier | Assign |
|---|---|
| principal | architecture, ambiguous ownership, public APIs, migrations, security, integration |
| standard | isolated implementation with a clear contract and a bounded set of projects |
| economy | mechanical edits, fixtures, docs, inventory, and read-only evidence gathering |

If the host cannot select models or reasoning effort, keep its default. Tool access
is a separate axis: evidence gathering, scouting, and review get read-only tools
where the host offers them. Never downgrade the root integration pass or final
release gate.

To prove something, brute-force it at economy:

- Fork one read-only job per claim, input, or variant, and run them all at once.
  Give each a script check, so `magus job wait` can pass it.
- Spawn the `magus-scout` agent for each where your harness installed it.
- Each reports its command and output ref.
- The principal tier only reconciles the claims where workers disagree.

Keep economy jobs short-lived. The brief points at evidence (paths, output refs, the
row) and carries no transcript. Where the brief names that size, the
worker reports and stops before passing it.

Nesting is allowed when the host supports it, but it creates no new budget and no
private ownership map. A child reports its proposed descendants to its parent before it
spawns them.

Descendants inherit the ancestor's deny paths and may subdivide only its write paths. A
cap the user sets applies to the whole tree, not once per parent.

Keep one integration owner per landing checkout, the integrator, never the root. A
child coordinates its descendants but may not accept changes outside its own job or
relax the root's criteria.

## Seed the partition with magus

Choose the target that will validate the work. `ci` is the release gate, but any
target `magus affected <target>` accepts can be planned:

```sh
magus affected <target> --plan
```

For a proposed change whose paths are known but not yet edited, plan those paths
instead of the current diff (`--stdin --plan`, one repo-relative path per line).

Read the JSON fields `count`, `max_parallel`, `source`, and `matrix`.
Use a shard only as the first partition. If paths are unknown, query the task's symbols,
files, and projects before producing a stdin plan.

## Prove that jobs do not collide

Classify the union of every job's proposed paths in one call:

```sh
magus describe file <both jobs' paths>... -o json
```

Read the facts:

- `overlaps` lists each declaration covering more than one proposed path: a shared
  write set by construction.
- `claims[].target` names the target that regenerates a path. Generated outputs have
  one integration owner and are never hand-edited by workers.
- `depends_on` carries the owner's direct edges.
- Affinity stays with `client` (`magus\insight`, read affinity). Without MCP,
  run `magus buzz -e` printing `magus\insight().affinity`.
- Use `magus refs <symbol>` when two jobs may touch the same API.

A read-only job has no write set, so it is outside this analysis.

Two jobs may run together only when:

- The combined classification reports no overlaps. Source write sets and declared
  outputs are disjoint, or share a file by claiming DIFFERENT declarations of it
  (`<file>#<declaration>`, the default). `--depends-on` is for the SAME one.
- Neither consumes an API or generated artifact the other changes.
- Shared manifests, lockfiles, schemas, and agent instructions have one owner. A
  magusfile or magus.yaml is shared by key claims (`magusfile.buzz#project.targets.test`).
- Dependency and temporal-affinity evidence does not say they should move together.

ONE WORKTREE PER WORKER wherever a write path touches workspace configuration: any
project's magusfile, its `magus.yaml`, or a spell source a magusfile imports.

magus READS those files to load the workspace. A half-saved one stops the workspace
loading for everybody in that checkout.
When `magus job fork` refuses one, the answer is a separate worktree, not a narrower
boundary: fork with `--checkout <dir>` outside the workspace, never by hand.

Fork records what it could prove in `write_proof` (`alone`, `disjoint` or
`overlapping`), and `magus ls jobs` prints it. An overlap is recorded, not refused:
sequencing two jobs onto one path is your call. Answer the guard's shared-checkout reminder with a proof or a
worktree, not a judgment that it looks fine.

Project boundaries alone are insufficient. A declared dependency means group the work
or serialize producer before consumer. Treat strong hidden affinity as a warning.
When evidence is incomplete, reduce parallelism.

## Fork the job, then spawn the worker

The order is fixed: fork the row, spawn the worker with a description naming it, and
only then does the worker take the lease. A spawn whose description names no live row
binds its child to nothing.

1. Record the checkpoint you are handing out: `magus vcs checkpoint -o name` prints
   the revision, plus a dirty-patch digest when the tree is not clean.
2. Fork one job per unit, from flags:

   ```sh
   magus job fork api-store --model <tier or model> \
     --criteria "accounts move to the new store; nothing names the old one" \
     --write-paths 'api/store.go,api/store_test.go,db/migrations/*.sql' \
     --check "go-test api -- -run TestStore" \
     --checkpoint <checkpoint>
   ```

   Or fork from a record with `magus job fork --stdin`, the form that carries goals;
   [reference.md](reference.md) shows one.

   Before every fork, run `magus job fork --stdin --dry-run`: it lists every problem with its code and fix, and writes nothing.
   `magus job fork --schema` prints every field and the newest `schema_version`.
3. Spawn the worker with the description `<parent>/<role> <job>`: two words, a parent id and a role joined by `/`, then the job id.
   - A root job has no parent row, so its first word takes any label
     (`orchestrator/feat api-store`).
   - A child reads `api-store/fix migrate`; the guard resolves the job as `migrate`,
     else `api-store/migrate`.
   - The role is a word for the work (`feat`, `fix`, `review`). A workspace spawn
     rule may restrict it, and its refusal names the list.
   - A title naming a row that no fork or apply validated is refused (`spawn-unvalidated`).
   - Resume first: follow-up on code a worker just wrote goes back to that worker as a message while its prompt cache is warm, at any context size; a fresh spawn is refused (`spawn-warm-worker`), and [reference.md](reference.md#resume-the-warm-worker) says when fresh is right.

Write paths name exact FILES; a glob too broad is refused as MGS3043 unless permitted. The
check is `<target> <project> [-- args]`, never the gate. [reference.md](reference.md)
covers claiming one declaration in a shared file and forking from a leased worker.

A fork refuses a job whose sources regenerate an output outside its write paths (MGS3044).
Add the output to the write paths, or permit it when the root regenerates it after integration.

Render the prompt FROM the row; never type it. `magus describe job <job>` prints the
job's own criteria, boundary and check, plus what the workspace knows and nobody wrote
down.

The guard grades every command a brief presents in a shell fence or a code span. One
it would deny refuses the spawn. A line that names a command to forbid it ("never ...") is not graded.

### What a brief leaves out

Brief carries only: JOB ID, goal and why, decisions made, evidence refs.
Leave out: skill loads, command style, `BAGGAGE` export, write paths, check, how to commit and push, attribution; guard, harness, `magus describe job` supply or enforce each.
Restated guard rule costs every worker tokens, goes stale. Worker keeps missing something -> fix guard rule or its message, never brief.
Follow-up message to bound worker: only what changed.

Every worker prompt includes its row, its JOB ID, relevant graph evidence, and
any cap the user set for the tree. Require the worker to:

- export `BAGGAGE=magus.lease=<its id>` before it works: the guard grades its writes only when that is
  set;
- preserve unrelated changes, stay inside write paths, and avoid generated outputs;
- run only its assigned magus target, in the foreground with a timeout that covers it, and never end its turn while a run is pending;
- never wait for the build budget. When a full budget refuses its check, it commits, reports that the check found no seat, and stops; you rerun it once one frees;
- never exit its row or report back while it waits on a reply from you: an answer that lands after the row ended reaches nobody.

End every brief with the step that files the result, as the worker's LAST act before
it reports back. A brief that leaves it out gets a prose report and a row that never
moves. The step: `magus job exit <job> --stdin` with a record of `schema_version` 2, `job`,
`changed_paths`, `validation` (`command` and `output_ref`), `descendants` and
`unresolved_risks`; `magus job exit --schema` prints the shape.
Keep unresolved risks mandatory.

A worker writes only its own row and the children it forks. Every other store write
is the orchestrator's; a refused worker reports it as an unresolved risk and stops.

After the spawn, the row carries the job:

- A worker records the base it LANDED on, which can differ from the checkpoint you
  handed it.
- A denied worker coordinates and never works around.
- Every row ends in pass, fail, or NO-RETURN, and the root writes which: silence is
  not a pass.
- Only a row with a check or a goal can pass. Fork a scout `--read-only`; it passes only
  through a script check (reference.md), and a prose scout ends no_return by design.

[reference.md](reference.md) covers bases, read paths, releasing a path, moving a live
job's boundary, how magus ends abandoned jobs, and what `magus job wait` checks.

## Declare the criteria magus can check for you

A GOAL is the part of a job's criteria magus grades itself, from evidence the worker
cannot author. It is a check that passed, or paths or symbols changed, present, absent
or unreferenced. Write goals in the record's `goals`. `magus job wait` refuses pass until
each verifies. [reference.md](reference.md) has the table and the rename pattern.

## Observe through the correct control plane

Run workers non-blocking; block on one only when your next action needs its result.
`magus status --watch=15s` is for a person, never an agent's background task: an agent waits
for its own run's completion notice.

The root reads `magus describe job <job>` or `magus job watch <job>` only when it needs to know.
Never message a worker to ask how it is going, only to CHANGE what it was handed.
[reference.md](reference.md) covers blocked workers and auditing what a job ran.

## Integrate and verify

The root never builds. The integrator is a role, never a check: spawned `<parent>/integrate
<job>` unisolated in the landing checkout, it cherry-picks, builds, regenerates and commits. As jobs finish:

1. Compare the store against the ACTUAL diff since each job's checkpoint, not the
   paths it reported (`magus graph diff --rev <revision>` for the domain). A differing
   dirty digest means it saw a tree you are not diffing.
2. Run `magus job wait <job>` on each returned job BEFORE you read its result. It
   checks what is mechanical:
   - every changed path inside the declared write paths and outside the denied ones;
   - a change set that is not empty on a row that writes;
   - the descendants the store carries;
   - the output ref, bound to a run of THAT JOB'S OWN CHECK which the store recorded
     as PASSING.

   A job that verifies is recorded `pass`. A rejection exits 1 naming every violation;
   a result that will not decode exits 2. A holder does not verify its own job, or a
   sibling's.

   Then reopen the evidence yourself (`magus query output <ref>`). A worker's prose about its criteria is not that evidence.
3. Resolve cross-job API changes centrally; never assign the same seam twice.
4. Regenerate declared outputs once, after source work converges and before the full
   test run, or a drift test fails for nothing.
5. Re-run `magus affected <target> --plan` over the actual diff. If its shape
   invalidates the original partition, stop parallel integration and reconcile.
6. Read the integrated changeset with `magus diff --impact` before landing it.
   It is context, never a verdict: nothing gates on it.
7. Validate once per wave, not once per fix:
   - After a failed full run, read it (`magus query output <ref>`). Rerun only the
     failing tests, with the runner's filter after `--`, until green; `magus-run`
     lists one per runner. Then run the full suite once.
   - Name a test that fails for an outside reason once, and do not rerun it.
   - A failing set that repeats unchanged stops the wave and goes to the person.
8. Run `magus affected ci` once, at the end, and evaluate the top-level acceptance criteria.
````


</section>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-multi-agent-full/SKILL.md
```

````markdown
# Splitting work across agents

Count the WRITE SETS your change needs (the distinct groups of files to edit), not the
projects it invalidates. Getting this backwards is the standard fan-out failure: a
one-line edit in a central package invalidates half the workspace and is still one
edit, and fanning it out produces several agents editing one file.

`magus affected <target> --plan` partitions VALIDATION: which targets to run, grouped
for runner balance. It is neither an edit assignment nor a proof of write isolation.
So it can veto a fan-out and never license one. One shard means keep the work local.
Several mean the testing parallelizes, and the editing is still your question.

Before any edits exist, the shard plan is empty and proves nothing. Finding the
candidate paths IS the partitioning work, done with the graph, not by intuition:

```sh
magus graph build                           # first in a fresh worktree, or refs answers "unknown, not absent"
magus refs <symbol> --occurrences -o json   # every edit site, column-precise
magus explain <node>                        # one node's edges and blast radius
magus affected ci --plan --stdin            # plan PROPOSED paths, before editing
```

You do not need to be asked to split work. "Split this across agents" is one way in;
several disjoint write sets you can name is another.

Fan-out is not inherently expensive. What costs is unbounded fan-out:
workers that hand work on without a shrinking scope, jobs with no acceptance criteria
so nobody can say when to stop, and a principal model assigned to mechanical edits. Each of those is a choice made below, not a property of fanning out. Say what a round
costs when the user is deciding, and prefer the smallest fan-out that covers the work.

Fan out only after the collision check below REPORTS the jobs disjoint; looking
separate is not that check. With only one coherent write set, keep the work local.

The root agent owns the goal, the budget, the topology, integration and final
verification, and never hands those out. Above the root is the human in the loop. What
lands, what is published and what a round may spend are the person's calls: the root
reports to them and does not decide them.

While a row it handed out is live, the root does no groundwork: no reading, searching or editing
of source, no runs of its own. It plans from the graph and moves jobs; a scout and integration are jobs.
The guard enforces this by role, and the role is the live lease: the root may take ONE row with
`magus job exec <row>` until `magus job exit <row>` ([the root's role](reference.md#the-roots-role)).

## Contents

- Coalesce what the write sets allow
- Run the graph-engineering loop
- Declare the interface before any job forks
- Set one topology boundary
- Seed the partition with magus
- Prove that jobs do not collide
- Fork the job, then spawn the worker
- Declare the criteria magus can check for you
- Observe through the correct control plane
- Integrate and verify

## Coalesce what the write sets allow

Disjoint write sets LICENSE parallelism; they do not require it. Every worker carries
a fixed load before it reads a line of the diff: a system
prompt, the repository's instruction file, the routing index, the skills that
load, and the graph queries it runs to find its own footing, spent identically
whether the unit is fifty lines or five hundred. So after partitioning,
ask whether each unit is big enough to be worth a worker.

- A depends-on chain is ONE worker in sequence, not two workers in turn.
  Two is two loads for one unit of work, and the second starts by rediscovering
  what the first just learned.
- For the rest, read the grouping lens (`magus\insight` through `client`,
  or `grouping history` in `magus describe job <job>`) and weigh each measured shape
  against the others: chained, split and solo each report the load before a
  first write and the context, wall time and collisions per group. It reports
  and never decides; a person writes any standing grouping policy.
- Where the lens answers unknown, apply these defaults and say in the plan that you
  did. Merge small disjoint units inside one project. Spawn only when more than one
  independent unit survives the merge. Fill idle root time from that pool, never by
  splitting finer.

## Run the graph-engineering loop

Prove it before you plan it. A claim a plan or brief rests on cites the output ref of
a run that settled it. A guess nobody ran stays out of the plan.

Graph engineering is a natural evolution of loop engineering: the graph improves
the loop's partition and collision decisions, and does not replace the loop. The loop:
state the goal and acceptance criteria, partition, hand out, observe, evaluate against
the evidence, revise ownership or ordering, then integrate.

Acceptance evidence is an output ref the root reopens (`magus query output <ref>`),
never a worker's prose. A worker that ran a filtered subset, or quietly restated its
criteria into something it did pass, reports success either way, and a
transcript cannot tell you which happened.

An agent may report that its edits are done, but no job is complete until its
acceptance criteria and assigned check pass. The root agent, not a worker,
decides whether the top-level goal is complete.

## Declare the interface before any job forks

When a change adds a shared API, the ROOT names it before any edit. A shared
API is a module several call sites use, a type, an event, or an exported
function. Name its path, every exported name with its signature, and the EXISTING
symbols it must reuse, not restate.

Workers implement the names they were handed and never coin a public one. A name
invented at each call site is how one concept ends up with five spellings, and how a plan that says "collapse two state types into one" ships
a third: each site made a locally sensible choice and nobody owned the set.

Then make the declaration gradeable, so the names are a contract, not a suggestion:

- `symbol` + `present` for each new exported name.
- `symbol` + `absent` for the name a second copy would predictably take beside a
  symbol the work must reuse. No gate grades reuse itself: `present`
  holds for a symbol that existed before the job began, and a reference count
  cannot tell the defining file or an import from real use.
- `symbol` + `unreferenced` for each helper the shared API replaces.

A job that adds or changes an exported API gets a `symbol` + `signature` goal by
default. A stub job lands the stub, and the root runs `magus graph build`. The root
then forks the `signature` goal empty. The fork records the indexed stub as the
contract, beside a `changed` goal on the same name.

A job's `breaks` list names the flags, JSON keys, exported symbols or stored formats
you approved it breaking. A worker that finds a break outside it stops and reports.
Each unreferenced symbol `magus job fork` lists in the write paths
(`deletion_candidates`) or `magus job wait` reports is finished or deleted. Confirm
first: the graph cannot see reflection, interface dispatch or outside consumers.

Check each declared name with `magus refs` before forking. A bare name that resolves
to more than one definition is graded against one of them, silently.

This applies with zero workers. A solo change across several call sites is a one-job
tree. Skipping the declaration because nothing is forked is the same failure without
a fork to blame. Write the table in the conversation, check it with `magus refs` when
the edits land, and only then report the change done.

## Set one topology boundary

Before spawning, state the topology: the model per job, and whether isolated worktrees are available.

- Fan-out and depth are not capped unless the workspace sets a cap. Spawn as many
  jobs, nested as deep, as the partition supports.
- A limit exists only when magus.yaml's `jobs` section sets one. Read it with
  `magus config view` or `config`.
  - `max_depth` and `max_live` make `magus job fork` refuse past them, naming the key.
  - `default_timeout` bounds a fork that names no `--timeout`.
  - Honor a cap the user states the same way.
- Editing costs the workspace nothing; VALIDATION contends: the magus runs a job
  triggers, which share the machine's build budget. The root waits for that budget,
  never a worker; a spawn or `magus job fork` into a full one says how full it is.
  Serialize validations that share a worktree, even when their write sets are disjoint.

Assign the check from the pipeline the workspace composed, not from convention.
`magus describe target ci <project>` names what `ci` chains and in what order.

- A job gets the narrowest check that covers its edits: the target from that
  decomposition. Scope it to the job's own tests with the runner's filter after `--`
  where one exists. Never the full suite.
- The integrator re-runs the described order, and `magus affected ci` re-proves the
  whole composition. A worker hand-sequencing lint, format, and test is re-deriving an
  order the magusfile already owns, and the step it forgets fails silently by
  omission.

A job's check is that narrow target, never the gate. The root gates ONCE, in its own
tree, after every unit lands, which is what stops seven fanned-out workers from
each running the whole pipeline concurrently on one machine.

Decide each job's validation PLANE with its target. Some worker environments cannot
execute magus at all. Examples are an isolated tree with no usable binary, or a guard
routing raw tools to targets it cannot run. Such a worker cannot validate what it
writes. Mark that job's check ROOT-DEFERRED when you fork it:

- the worker writes the tests, stops at the static checks its environment does run,
  and says so;
- the root executes the job's target centrally before verifying. Leaving each worker to discover the wall
  spends its budget once per worker, and its report reads "done" with nothing executed,
  which `magus job wait` refuses to record as a pass.

A worker may fork part of its own job. It may not hand it out without shrinking the
problem; that is the shape that does not terminate, and
the cost people attribute to "multi-agent" is almost always this. These instructions give it a
definitive end without capping depth:

- **Every level narrows.** A child's scope is a strict subset of its parent's. A
  worker that would hand on its whole job does the work instead.
- **Every job carries acceptance criteria down.** A child inherits its parent's
  criteria plus its own. A job nobody can evaluate cannot end; that, not the nesting,
  makes depth dangerous.
- **A retry is `magus job fork <same id>`, never a new id.** The next holder reads the previous
  attempt in `magus describe job`, never a transcript. One marked not retryable names
  what changes first (`magus job apply` to widen, a config edit, or you). Make it, or
  do the job locally; never re-fork it unchanged.

  A goal unmet twice marks it so: the root re-scopes the job, or serializes it behind
  whatever keeps breaking it. Two jobs with an undeclared dependency each break
  the other's criteria, and re-forking the failing one satisfies every instruction above
  while alternating forever. The budget is what ends it.
- **Whatever the parent does not hand out, the parent still owns.** A strict subset
  leaks by construction. Split "no caller of X remains" into per-project jobs, and
  the callers in no project belong to nobody. Every job passes and the goal is
  unmet. Carry a remainder row at each level and close it explicitly.

START EVERY SPAWN AT THE ECONOMY TIER, and SAY which model you picked. Move a job up
only for work the table below assigns higher. The fit still runs both ways: an
ambiguous API boundary does not get the cheapest model because it looked like less
work. Judge cost per COMPLETED job: a cheap worker that needs a rerun is not
cheaper.

Naming it is the checkable half: every spawn names a model, or an agent definition
that names one.

- Inheriting the parent's model ON PURPOSE is fine and often right. A hard review
  under a cheaper coordinator is exactly the case a weaker-only ordering would forbid.
- Inheriting it BY OMISSION is the failure. A host defaulting to "same as the parent"
  makes every unnamed spawn the most expensive one, and records no choice.
  Measured 2026-09-15: five of nine workers that inherited the root's model did mechanical
  work a cheaper model does as well.

When the tier is unclear, start at economy. A job that fails its criteria there is
re-forked one tier up, not retried at the same one. "Only ever spawn something
weaker" was tried and withdrawn, because same-strength offload is legitimate, so this
is a starting point and not a ceiling.

Tiers are the workspace's. A magusfile enum (`magus\job.modelTiers`, shipped as economy,
standard, principal) maps each to every harness's model alias. `--model` takes a tier or a
model. The row records tier, rank, source (row, matrix, default) and why, and the guard
compares a spawn's tier by rank. magus never picks a model live: re-fork to change a tier.

| Shipped tier | Assign |
|---|---|
| principal | architecture, ambiguous ownership, public APIs, migrations, security, integration |
| standard | isolated implementation with a clear contract and a bounded set of projects |
| economy | mechanical edits, fixtures, docs, inventory, and read-only evidence gathering |

If the host cannot select models or reasoning effort, keep its default. Tool access
is a separate axis: evidence gathering, scouting, and review get read-only tools
where the host offers them. Never downgrade the root integration pass or final
release gate.

To prove something, brute-force it at economy. Many economy workers finish about as
fast as one principal worker, for far fewer tokens:

- Fork one read-only job per claim, input, or variant, and run them all at once.
  Give each a script check, so `magus job wait` can pass it.
- Spawn the `magus-scout` agent for each where your harness installed it.
- Each reports its command and output ref, so evidence settles the claim and not a worker's reading.
- The principal tier only reconciles the claims where workers disagree.

Keep economy jobs short-lived. The brief points at evidence (paths, output refs, the
row) and carries no transcript. A worker resends its whole context every turn, and some
providers raise a tier's price past a prompt size. Where the brief names that size, the
worker reports and stops before passing it.

Nesting is allowed when the host supports it, but it creates no new budget and no
private ownership map. A child reports its proposed descendants to its parent before it
spawns them. The store carries each with its parent, model, criteria and write paths.

Descendants inherit the ancestor's deny paths and may subdivide only its write paths. A
cap the user sets applies to the whole tree, not once per parent.

Keep one integration owner per landing checkout, the integrator, never the root. A
child coordinates its descendants but may not accept changes outside its own job or
relax the root's criteria.

## Seed the partition with magus

Choose the target that will validate the work. `ci` is the release gate, but any
target `magus affected <target>` accepts can be planned:

```sh
magus affected <target> --plan
```

For a proposed change whose paths are known but not yet edited, plan those paths
instead of the current diff (`--stdin --plan`, one repo-relative path per line).

```sh
printf '%s\n' <repo-relative-path>... | magus affected <target> --stdin --plan
```

Read the JSON fields `count`, `max_parallel`, `source`, and `matrix`. A shard is a
history-balanced execution group, not an edit assignment or proof of write isolation.
Use a shard only as the first partition. If paths are unknown, query the task's symbols,
files, and projects before producing a stdin plan.

## Prove that jobs do not collide

Classify the union of every job's proposed paths in one call:

```sh
magus describe file <both jobs' paths>... -o json
```

Read the facts:

- `overlaps` lists each declaration covering more than one proposed path: a shared
  write set by construction.
- `claims[].target` names the target that regenerates a path. Generated outputs have
  one integration owner and are never hand-edited by workers.
- `depends_on` carries the owner's direct edges.
- Affinity stays with `client` (`magus\insight`, read affinity). Without MCP,
  run `magus buzz -e` printing `magus\insight().affinity`.
- Use `magus refs <symbol>` when two jobs may touch the same API.

A read-only job has no write set, so it is outside this analysis.

Two jobs may run together only when:

- The combined classification reports no overlaps. Source write sets and declared
  outputs are disjoint, or share a file by claiming DIFFERENT declarations of it
  (`<file>#<declaration>`, the default). `--depends-on` is for the SAME one.
- Neither consumes an API or generated artifact the other changes.
- Shared manifests, lockfiles, schemas, and agent instructions have one owner. A
  magusfile or magus.yaml is shared by key claims (`magusfile.buzz#project.targets.test`).
- Dependency and temporal-affinity evidence does not say they should move together.

ONE WORKTREE PER WORKER wherever a write path touches workspace configuration: any
project's magusfile, its `magus.yaml`, or a spell source a magusfile imports.

magus READS those files to load the workspace. A half-saved one stops the workspace
loading for everybody in that checkout at once. The rest lose `magus run`, `magus ls` and their own tests over an edit they
cannot see. Measured 2026-09-17: one half-saved magusfile stopped three workers
sharing a checkout.
When `magus job fork` refuses one, the answer is a separate worktree, not a narrower
boundary: fork with `--checkout <dir>` outside the workspace, never by hand.

Fork records what it could prove in `write_proof` (`alone`, `disjoint` or
`overlapping`), and `magus ls jobs` prints it. An overlap is recorded, not refused:
sequencing two jobs onto one path is your call, and the row records
whether anybody checked. Answer the guard's shared-checkout reminder with a proof or a
worktree, not a judgment that it looks fine.

Project boundaries alone are insufficient. A declared dependency means group the work
or serialize producer before consumer. Treat strong hidden affinity as a warning.
When evidence is incomplete, reduce parallelism.

## Fork the job, then spawn the worker

The order is fixed: fork the row, spawn the worker with a description naming it, and
only then does the worker take the lease. A spawn whose description names no live row
binds its child to nothing, so every write it makes is graded as
an editor magus cannot attribute.

1. Record the checkpoint you are handing out: `magus vcs checkpoint -o name` prints
   the revision, plus a dirty-patch digest when the tree is not clean.
2. Fork one job per unit, from flags:

   ```sh
   magus job fork api-store --model <tier or model> \
     --criteria "accounts move to the new store; nothing names the old one" \
     --write-paths 'api/store.go,api/store_test.go,db/migrations/*.sql' \
     --check "go-test api -- -run TestStore" \
     --checkpoint <checkpoint>
   ```

   Or fork from a record with `magus job fork --stdin`, the form that carries goals;
   [reference.md](reference.md) shows one.

   Before every fork, run `magus job fork --stdin --dry-run`: it lists every problem with its code and fix, and writes nothing.
   `magus job fork --schema` prints every field and the newest `schema_version`.
3. Spawn the worker with the description `<parent>/<role> <job>`: two words, a parent id and a role joined by `/`, then the job id.
   - A root job has no parent row, so its first word takes any label
     (`orchestrator/feat api-store`).
   - A child reads `api-store/fix migrate`; the guard resolves the job as `migrate`,
     else `api-store/migrate`.
   - The role is a word for the work (`feat`, `fix`, `review`). A workspace spawn
     rule may restrict it, and its refusal names the list.
   - A title naming a row that no fork or apply validated is refused (`spawn-unvalidated`).
   - Resume first: follow-up on code a worker just wrote goes back to that worker as a message while its prompt cache is warm, at any context size; a fresh spawn is refused (`spawn-warm-worker`), and [reference.md](reference.md#resume-the-warm-worker) says when fresh is right.

Write paths name exact FILES; a glob too broad is refused as MGS3043 unless permitted. The
check is `<target> <project> [-- args]`, never the gate. [reference.md](reference.md)
covers claiming one declaration in a shared file and forking from a leased worker.

A fork refuses a job whose sources regenerate an output outside its write paths (MGS3044).
Add the output to the write paths, or permit it when the root regenerates it after integration.

Render the prompt FROM the row; never type it. `magus describe job <job>` prints the
job's own criteria, boundary and check, plus what the workspace knows and nobody wrote
down: the projects the write paths reach, the declared
output globs that land inside them, the paths a sibling job is holding, the build
inputs and workspace configuration that have one owner, and the projects that
change alongside the leased ones without declaring a dependency. Two renders of one
row are byte-identical, which hand-typed prompts are not: seven of
them disagreed about a dedup key and every one ended with the gate.

The guard grades every command a brief presents in a shell fence or a code span. One
it would deny refuses the spawn, since the worker runs a brief's
commands as written. A line that names a command to forbid it ("never ...") is not graded.

### What a brief leaves out

A brief carries the JOB ID, the goal and why, the decisions made, and evidence refs.
It leaves out skill loads, command style, the `BAGGAGE` export, write paths, the check,
how to commit and push, and attribution: the guard, the harness and `magus describe job`
supply or enforce each, and a refusal says what to do. Restated, a guard rule costs every
worker tokens, goes stale, and crowds out the model's own judgment. When a worker keeps
missing something, fix the guard rule or its message, not the brief. A follow-up message
to a bound worker carries only what changed.

Every worker prompt includes its row, its JOB ID, relevant graph evidence, and
any cap the user set for the tree. Require the worker to:

- export `BAGGAGE=magus.lease=<its id>` before it works: that is the W3C
  Baggage channel, and the member is what tells the agent guard whose declared boundary
  to grade a write against, so a worker that never exports it is graded as an editor
  magus cannot attribute. Export `TRACEPARENT` too when your host has one, and add
  `magus.spawner=<your label>` to the baggage: magus records the trace, the parent span
  and the label as CLAIMS, so `magus session ls` can show who spawned whom, and no
  verdict is ever keyed on them;
- preserve unrelated changes, stay inside write paths, and avoid generated outputs;
- run only its assigned magus target, in the foreground with a timeout that covers it, and never end its turn while a run is pending;
- never wait for the build budget. When a full budget refuses its check, it commits, reports that the check found no seat, and stops; you rerun it once one frees;
- never exit its row or report back while it waits on a reply from you: an answer that lands after the row ended reaches nobody.

End every brief with the step that files the result, as the worker's LAST act before
it reports back. A brief that leaves it out gets a prose report and a row that never
moves: the store keeps the write paths held, `magus job wait`
has nothing to verify, and nobody notices until a sibling is refused over a path
the finished worker no longer needs. The step: `magus job exit <job> --stdin` with a record of `schema_version` 2, `job`,
`changed_paths`, `validation` (`command` and `output_ref`), `descendants` and
`unresolved_risks`; `magus job exit --schema` prints the shape, and
[reference.md](reference.md) shows a record.
A typed result makes verification mechanical. Keep unresolved risks mandatory, so a
mis-scoped worker can say so instead of widening silently.

A worker writes only its own row and the children it forks. Every other store write
is the orchestrator's; a refused worker reports it as an unresolved risk and stops.

After the spawn, the row carries the job:

- A worker records the base it LANDED on, which can differ from the checkpoint you
  handed it.
- A denied worker coordinates and never works around.
- Every row ends in pass, fail, or NO-RETURN, and the root writes which: silence is
  not a pass.
- Only a row with a check or a goal can pass. Fork a scout `--read-only`; it passes only
  through a script check (reference.md), and a prose scout ends no_return by design.

[reference.md](reference.md) covers bases, read paths, releasing a path, moving a live
job's boundary, how magus ends abandoned jobs, and what `magus job wait` checks.

## Declare the criteria magus can check for you

A GOAL is the part of a job's criteria magus grades itself, from evidence the worker
cannot author. It is a check that passed, or paths or symbols changed, present, absent
or unreferenced. Write goals in the record's `goals`. `magus job wait` refuses pass until
each verifies. [reference.md](reference.md) has the table and the rename pattern.

## Observe through the correct control plane

Run workers non-blocking; block on one only when your next action needs its result.
`magus status --watch=15s` is for a person, never an agent's background task: an agent waits
for its own run's completion notice.

The root reads `magus describe job <job>` or `magus job watch <job>` only when it needs to know.
Never message a worker to ask how it is going, only to CHANGE what it was handed.
[reference.md](reference.md) covers blocked workers and auditing what a job ran.

## Integrate and verify

The root never builds. The integrator is a role, never a check: spawned `<parent>/integrate
<job>` unisolated in the landing checkout, it cherry-picks, builds, regenerates and commits. As jobs finish:

1. Compare the store against the ACTUAL diff since each job's checkpoint, not the
   paths it reported (`magus graph diff --rev <revision>` for the domain). A differing
   dirty digest means it saw a tree you are not diffing.
2. Run `magus job wait <job>` on each returned job BEFORE you read its result. It
   checks what is mechanical:
   - every changed path inside the declared write paths and outside the denied ones;
   - a change set that is not empty on a row that writes;
   - the descendants the store carries;
   - the output ref, bound to a run of THAT JOB'S OWN CHECK which the store recorded
     as PASSING. There is no `passed` field on the result: wait reads the
     ref's own recorded attempt from the output store and derives the outcome from
     it, never from what the worker claims.

   A job that verifies is recorded `pass`. A rejection exits 1 naming every violation;
   a result that will not decode exits 2. A holder does not verify its own job, or a
   sibling's.

   Then reopen the evidence yourself (`magus query output <ref>`). Wait proves the ref
   names a passing run of this job's own check, never that the work meets the job's
   GOAL. A worker's prose about its criteria is not that evidence.
3. Resolve cross-job API changes centrally; never assign the same seam twice.
4. Regenerate declared outputs once, after source work converges and before the full
   test run, or a drift test fails for nothing.
5. Re-run `magus affected <target> --plan` over the actual diff. If its shape
   invalidates the original partition, stop parallel integration and reconcile.
6. Read the integrated changeset with `magus diff --impact` before landing it.
   It shows reach, other recent editors, a rebuild estimate from recorded run times,
   advisors and anchored notes. It is context, never a verdict: nothing gates on it. An
   empty section means nobody could measure it, not that nothing was found.
7. Validate once per wave, not once per fix:
   - After a failed full run, read it (`magus query output <ref>`). Rerun only the
     failing tests, with the runner's filter after `--`, until green; `magus-run`
     lists one per runner. Then run the full suite once.
   - Name a test that fails for an outside reason once, and do not rerun it.
   - A failing set that repeats unchanged stops the wave and goes to the person.
8. Run `magus affected ci` once, at the end, and evaluate the top-level acceptance criteria.
````


</section>

</article>
