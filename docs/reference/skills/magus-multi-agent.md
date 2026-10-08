---
title: magus-multi-agent
generated_from: internal/agent/skills/magus-multi-agent/SKILL.md
description: "Load BEFORE your first subagent spawn in a magus workspace: an Agent or Task tool call, a background worker, parallel workers, fanning out, or delegating part of a task."
tags: [agents, skills, magus-multi-agent]
skill_full_bytes: 39849
skill_short_bytes: 28631
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
| `agent-skill-version` | `114` |
| `knowledge-schema-version` | `16` |
| `skill-content` | `e883742deda8` |
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
Several mean the testing parallelizes, and the editing is still your question.

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

Hand every spawn a job row forked first, read-only scouts included. A child bound to
no row is graded as you are, and no lease bounds you. "Fork the job, then spawn the
worker" below has the commands.

Fan-out is not inherently expensive. Say what a
round costs when the user is deciding, and prefer the smallest fan-out that covers
the work.

Fan out only after the collision check below REPORTS the jobs disjoint; looking
separate is not that check. With only one coherent write set, keep the work local.
The root agent owns the goal, the budget, the topology, integration, and final
verification, and never hands those out.

## Coalesce what the write sets allow

Disjoint write sets LICENSE parallelism; they do not require it. Every worker carries
a fixed load before it reads a line of the diff. So after partitioning, ask whether
each unit is big enough to be worth a worker.

Apply these four in the order they bite:

- A depends-on chain is ONE worker in sequence, not two workers in turn.
- Merge small disjoint units inside one project. Splitting them usually gains less
  wall clock than the load you pay twice.
- Spawn only when more than one independent BIG unit survives the merge. One unit is
  inline work.
- Fill idle root time from that pool, never by splitting finer. A root blocked on a
  gate is a reason to start the next merged unit.

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
function. Name:

- the path;
- every exported name with its signature;
- the EXISTING symbols it must reuse, not restate.

Workers implement the names they were handed and never coin a public one. A name
invented at each call site is how one concept ends up with five spellings.

Then make the declaration gradeable, so the names are a contract, not a suggestion:

- `symbol` + `present` for each new exported name.
- `symbol` + `absent` for the name a second copy would predictably take beside a
  symbol the work must reuse. No gate grades reuse itself.
- `symbol` + `unreferenced` for each helper the shared API replaces.

Check each declared name with `magus refs` before forking. A bare name that resolves
to more than one definition is graded against one of them, silently.

This applies with zero workers. A solo change across several call sites is a one-job
tree. Skipping the declaration because nothing is forked is the same failure.

## Set one topology boundary

Before spawning, state the topology: the model per job, and whether isolated
worktrees are available.

- Fan-out and depth are not capped unless the workspace sets a cap. Spawn as many
  jobs, nested as deep, as the partition supports.
- A limit exists only when magus.yaml's `jobs` section sets one. Read it with
  `magus config view` or `config`.
  - `max_depth` and `max_live` make `magus job fork` refuse past them, naming the key.
  - `default_timeout` bounds a fork that names no `--timeout`.
  - Honor a cap the user states the same way.
- Editing costs the workspace nothing; VALIDATION contends: the magus runs a job
  triggers. Read the live pool (`magus status`) before starting a validation, not
  before starting an agent. Serialize validations that share a worktree, even when
  their write sets are disjoint.

Assign the check from the pipeline the workspace composed, not from convention.
`magus describe target ci <project>` names what `ci` chains and in what order.

- A job gets the narrowest target from that decomposition.
- The integrator re-runs the described order, and `magus affected ci` re-proves the
  whole composition.

A job's check is that narrow target, never the gate. The root gates ONCE, in its own
tree, after every unit lands.

Decide each job's validation PLANE with its target. Some worker environments cannot
execute magus at all. Examples are an isolated tree with no usable binary, or a guard
routing raw tools to targets it cannot run. Such a worker cannot validate what it
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
  criteria plus its own. A job nobody can evaluate cannot end; that, not the nesting,
  makes depth dangerous.
- **A job that fails its criteria twice is not re-issued.** The root does it locally,
  or serializes it behind whatever keeps breaking it.
- **Whatever the parent does not hand out, the parent still owns.** A strict subset
  leaks by construction. Split "no caller of X remains" into per-project jobs, and
  the callers in no project belong to nobody. Every job passes and the goal is
  unmet. Carry a remainder row at each level and close it explicitly.

Pick the model that FITS the job, and SAY which one. The fit runs both ways. A
mechanical rename does not need the strongest model. An ambiguous API boundary
does not get the cheapest one because it looked like less work.

Naming it is the checkable half: every spawn names a model, or an agent definition
that names one.

- Inheriting the parent's model ON PURPOSE is fine and often right.
- Inheriting it BY OMISSION is the failure. A host defaulting to "same as the parent"
  makes every unnamed spawn the most expensive one, and records no choice.

ASK THE HUMAN when the right model is unclear, before spawning.

Model NAMES belong to the host, never to magus. Name the model your host names, or an
agent definition the user owns.

Map work to provider capabilities without assuming model names:

| Model | Assign |
|---|---|
| principal | architecture, ambiguous ownership, public APIs, migrations, security, integration |
| standard | isolated implementation with a clear contract and a bounded set of projects |
| economy | mechanical edits, fixtures, docs, inventory, and read-only evidence gathering |

If the host cannot select models or reasoning effort, keep its default. Tool access
is a separate axis: evidence gathering, scouting, and review get read-only tools
where the host offers them. Never downgrade the root integration pass or final
release gate.

Nesting is allowed when the host supports it, but it creates no new budget and no
private ownership map.

- Before a child spawns descendants, it reports the proposed jobs to its parent.
- The store then carries those descendants, their parent, model, criteria, and write
  paths.
- Descendants inherit the ancestor's deny paths and may subdivide only the ancestor's
  write paths.
- A cap the user sets applies to the whole tree, not once per parent.

Keep one integration owner at the root, however deep the job tree.
A child coordinates its descendants but may not relax the root's criteria.

## Seed the partition with magus

Choose the target that will validate the work. `ci` is the release gate, but any
target `magus affected <target>` accepts can be planned:

```sh
magus affected <target> --plan
```

For a proposed change whose paths are known but not yet edited, plan those paths
instead of the current diff:

```sh
printf '%s\n' <repo-relative-path>... | magus affected <target> --stdin --plan
```

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

- The combined classification reports no overlaps: source write sets and declared
  outputs disjoint.
- Neither consumes an API or generated artifact the other changes.
- Shared manifests, lockfiles, schemas, workspace configuration, and agent
  instructions have one owner.
- Dependency and temporal-affinity evidence does not say they should move together.

ONE WORKTREE PER WORKER wherever a write path touches workspace configuration: any
project's magusfile, its `magus.yaml`, or a spell source a magusfile imports.

magus READS those files to load the workspace. A half-saved one stops the workspace
loading for everybody in that checkout at once.
When `magus job fork` refuses one, the answer is a separate worktree, not a narrower
boundary.

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
   magus job fork api-store --model <model> \
     --criteria "accounts move to the new store; nothing names the old one" \
     --write-paths 'api/store.go,api/store_test.go,db/migrations/*.sql' \
     --check "go-test api -- -run TestStore" \
     --checkpoint <checkpoint>
   ```

   Or fork from a record with `magus job fork --stdin`, the form that carries goals
   (see the next section).

   `magus job fork --schema` prints every field and the newest `schema_version`.
3. Spawn the worker with the description `<parent>/<role> <job>`: two words, the
   first a parent id and a role joined by `/`, the second the job id.
   - A root job has no parent row, so its first word takes any label
     (`orchestrator/feat api-store`).
   - A child reads `api-store/fix migrate`; the guard resolves the job as `migrate`,
     else `api-store/migrate`.
   - The role is a word for the work (`feat`, `fix`, `review`). A workspace spawn
     rule may restrict it, and its refusal names the list.

Write paths name FILES: a file, a file the job creates, or a file glob
(`internal/queue/*.go`, `docs/**/*.md`). A directory, a project root, or a glob that
matches one (`api`, `internal/**`) is refused with [MGS3018](https://eli.gladman.cc/magus/reference/codes/sandbox/MGS3018/).

Two jobs share one file by claiming declarations in it: `<file>#<declaration>`, such
as `internal/agent/catalog.go#SkillVersion`.

- A claim names one file, never a glob.
- The file needs a diff driver (`magus doctor` lists the managed ones); otherwise
  fork refuses with [MGS3031](https://eli.gladman.cc/magus/reference/codes/sandbox/MGS3031/).
- Two jobs that must edit the SAME declaration still have one owner. Give it to one
  job, or order them with `--depends-on`.

The check is `<target> <project> [-- args]` with the `magus run` implied (`"test ."`,
`"go-test api -- -run TestStore"`), never the gate or a target that chains to it.

Fork with `client` (`magus\job.put`) from an agent, or `magus job fork` from a
terminal. A worker holding a lease forks its own units the same way, naming its
job with `--parent` and paths inside its own.

Render the prompt FROM the row; never type it. `magus describe job <job>` prints the
job's own criteria, boundary and check, plus what the workspace knows and nobody wrote
down. Two renders of one
row are byte-identical, which hand-typed prompts are not.

The guard grades every command a brief presents in a shell fence or a code span. One
it would deny refuses the spawn. A line that names a command to forbid it ("never ...") is not
graded.

Every worker prompt includes its row, its JOB ID, relevant graph evidence, and
any cap the user set for the tree. Require the worker to:

- export `BAGGAGE=magus.lease=<its id>` before it works: the guard grades its writes only when that is
  set;
- preserve unrelated changes, stay inside write paths, and avoid generated outputs;
- run only its assigned magus target.

End every brief with the step that files the result, as the worker's LAST act before
it reports back. A brief that leaves it out gets a prose report and a row that never
moves. The step, with the shape `magus job exit --schema`
prints:

```sh
magus job exit <job> --stdin <<'EOF'
{"schema_version": 2, "job": "<job>",
 "changed_paths": ["api/store.go", "api/store_test.go"],
 "validation": {"command": "magus run go-test api -- -run TestStore", "output_ref": "<ref>"},
 "descendants": [],
 "unresolved_risks": ["what is left, or what the worker could not verify"]}
EOF
```

A typed result makes verification mechanical. Keep unresolved risks mandatory, so a mis-scoped
worker can say so instead of widening silently.

A worker writes only its own row and the children it forks. Every other store write
is the orchestrator's; a refused worker reports it as an unresolved risk and stops.

The checkpoint you recorded is what you HANDED the job; the base it LANDED ON is a
separate fact. Hosts that isolate workers in per-worker trees routinely branch them
from an older revision than the tree you partitioned.

- A worker whose spawn title names its job (`<parent>/<role> <job>`) is bound and
  records its base on its first call. Any other worker runs `magus job exec <its id>`
  once to record it.
- The guard binds the caller that ran it, keyed on the host's session and subagent
  ids. Workers sharing a tree each hold their own lease, and none binds you.
- The answer is a status on the row: match, revision-match (same revision, different
  uncommitted patch), diverged, or unknown. A reading names both tokens and the next
  step.
- It is a FACT, not a gate: every status records, diverged included.
- Acting on it is yours. Respawn from the right revision. Or have the worker
  materialize the files it builds on from the intended one, and re-fork the job
  with the right checkpoint. Materialize with `git show <rev>:<path> > <path>`, verifying
  each blob against `git rev-parse <rev>:<path>`.

Name any fact that will READ as drift to the worker's snapshot (a project deleted
this session, a rename, an index regenerated underneath it). Never write a generic
"expect drift" line; it primes the
worker to dismiss real anomalies.

Write paths bound READS too: a worker may read its projects and what they declare
`depends_on`. When a worker must READ something it must not WRITE, put that path in the
row's `read_paths` instead of widening `write_paths`. Widening is how two workers end up owning one file.

Ownership ends when EDITING ends, not when the worker exits. A worker done writing a
contested path announces the release at once, then carries on validating. It shrinks
the job's `write_paths` with another `client` (`magus\job.put`) write, or messages
the orchestrator if the host supports it.

That write records each dropped path with its digest at that moment. Hand the digest
to the job taking the path over. One that no longer matches at verification means the
waiter built on a tree the releaser never saw.

Moving a live job's boundary is yours. Change its record and run `magus job apply -f
<file>` (`-f -` reads stdin).

- The record is the whole spec: one write widens or revokes write paths and adds
  goals, and the job keeps its state.
- `--dry-run` prints the spec diff and writes nothing.
- A check you find the job owes mid-flight is a goal you add this way, so `magus job
  wait` grades it from a recorded run.
- A revoked path is recorded as a release with its digest. The worker's next write
  there is refused, naming the revocation.
- Ending a whole job stays `magus job exit <job>`. A worker never widens: its refusal
  names `magus describe job` and tells it to ask you.

Advance the row on every state change. `magus ls jobs` then shows which live jobs
claim intersecting `write_paths` and how long since each row was touched. A reported
overlap is a pair you either intended or must repartition.

magus ENDS a live job itself, as `no_return` with an `end_reason`, on every read of the
store when it can prove nobody holds it:

- an ancestor ended;
- the checkout `magus job exec` took it in no longer exists;
- it is still `declared`, nobody ever took it, and it was not updated within
  `jobs.stale_after` (default 2h, `0` for never).

Each ended job prints `ended <id>: <reason>` on stderr. So remove a worker's worktree
only once its job is done, and advance a root you are still using. What magus cannot
prove it leaves live, and ending those stays yours. A taken job that went quiet reads
`stale`, one past its timeout `overdue`, each with `magus job exit <id>`.

`--timeout <duration>` on fork is OPTIONAL and unset by default. Past it the guard
denies every write graded under that lease, and its paths stop blocking other jobs.
The row stays live until you end it. A job with acceptance criteria needs no bound.

`magus job wait` holds a writing job's `changed_paths` to the diff magus observes since
its checkpoint:

- Every claimed path must be in that diff, and something in it must be inside the
  write paths.
- A diff it cannot read FAILS, so fork writing jobs with a checkpoint.
- It refuses pass while any descendant is still live.
- It grades a child against its own symbol goals and its ancestors'.

The guard grades each write of a worker that exported `magus.lease` against these rows
and denies one outside them, naming the owner. A writer magus cannot attribute is only
ADVISED, and every uncertainty fails open: a seatbelt, not a sandbox.

So a denied worker COORDINATES and never works around. Ask the orchestrator to
re-partition, or have the owning job release the path, then retry. Step 1 of Integrate and verify checks the same boundary against
the checkpoint: the half that does not depend on a worker cooperating.

A read-only job carries an abbreviated row: no write paths, no deny paths. Every row
ends in pass, fail, or NO-RETURN, and the root writes which: silence is not a pass.

Make acceptance criteria observable: named tests, artifacts, diagnostics, API
behavior, or review checks. A child that hands work on evaluates its descendants
before reporting upward.

## Declare the criteria magus can check for you

A job's acceptance criteria are prose a reader grades. A GOAL is the part magus grades
itself, from evidence the worker cannot author. `magus job wait` refuses to record pass
until every one verifies.

- Goals are data: write them in the job record's `goals`, never as flags.
- `magus job fork` refuses a job that writes and declares neither a check nor a goal.
- `client` (`magus\job.put`) takes the same `goals` array.

A goal names WHAT it examines and what must be true of it:

| kind     | expects                                         | read from                                      |
| -------- | ----------------------------------------------- | ---------------------------------------------- |
| `check`  | `passed`                                        | a recorded run, captured after the declaration |
| `paths`  | `changed`, `present`, `absent`                  | the diff since the checkpoint, or the tree now |
| `symbol` | `changed`, `present`, `absent`, `unreferenced`  | the knowledge graph, below file granularity    |

One entry reads `{"id": "gone", "kind": "symbol", "expect": "absent", "symbols": ["LegacyAccountStore"]}`.
Each kind has a default expectation (`passed` for a check, `changed` otherwise), so the
common goal names only its kind and subject.

Reach for `symbol` + `unreferenced` when partitioning a rename: the REMAINDER instruction made
checkable. Split per project, and the callers in no project belong to no job. Every
job passes and the rename is unfinished.

- Grade it while the old name is still defined. The graph counts references into a
  definition; once the definition is gone, `unreferenced` FAILS rather than guess.
- The job that deletes it declares `absent` beside a check that builds the callers.

Every kind reads what magus already holds, which makes a goal a contract, not an
attestation. There is no goal for "this command exited 0". Declare a target and use `check`.

The worker's own `changed_paths` is its account, never the evidence. The diff and the
tree are read in the checkout that took the job, so wait from your own tree while the
worker's still exists. An observation magus could not MAKE fails the goal.

Ask where a job stands without advancing it:

```sh
magus describe job <job>
```

It prints where each goal stands beside the terms: the same grading `magus job wait`
does, recording nothing. Use it instead of asking a worker how it is going.

SEQUENCE goals with `depends_on` between them; a failed prerequisite propagates. Do
NOT nest them: a goal that wants children is a JOB that wants splitting.

Run workers non-blocking by default. Block on one only when your next action needs its
result. An agent spawned only to wait, poll, or repeat the root's discovery is not an
edit job and spends budget for nothing.

## Observe through the correct control plane

Track the job tree, agent state, messages, and completion in the provider's agent or
task view. Use it to keep the store aware of descendants.

Watch processes and shared workspace resources with magus:

```sh
magus status --watch=15s
```

It shows magus process state, lock holders, and shared-service state and adoption. It
does not show an agent thinking without running a magus process. Never replace it with
sleep loops, repeated `ps`, or a waiting agent.

To wait for a process you did not start to end, use your host's own wait or monitor
tool.

### "How is it going" is a read, never a message

Never message a worker to ask how it is doing. The question costs it the turn it was
in, and what comes back is its account of itself, not what happened. Three reads answer it without touching the
worker.

| you want | read |
| --- | --- |
| to hand the question to a person | the console link every verb that names a job prints |
| to watch it happen | `magus job watch <job>` |
| to know whether it is finished | `magus describe job <job>` |

`magus job watch` prints one line per event until you interrupt it. It merges files
changed under the job's write paths, the guard's tool calls under its lease, and the
runs recorded against it. A file is attributed by WRITE PATH and by nothing the worker says, so the worker cannot make it quiet.

Message a worker only to CHANGE what it was handed. Anything you merely want to KNOW
is one of the three reads above.

A blocked worker RAISES; it never stalls quietly.

- Piping the block to `magus session notify --outcome waiting` (blocked on input) or
  `--outcome permission` (blocked on approval) opens a durable request in this
  repository. No other outcome opens one.
- `magus session attention` lists what is open. `magus session attention -q` prints nothing and exits 1 on an empty
  queue: the form to test from a loop.
- Nothing closes a request by itself. The orchestrator, or any human, disposes it
  with `magus session dispose <id> --reason "<why>"`.
- A worker that raised one waits for the disposition; it never chooses for itself.

`magus session` is how the root audits what a job RAN, as opposed to what it reported.
`magus session --since 2h -o json` answers what the fleet has been doing.

Each
invocation carries its lease, its claimed spawner and parent span, and the targets
it ran with their outcomes. A worker in its own worktree is still listed.

Re-plan when nesting, dependencies, ownership, failing criteria, locks, or
services change. Update the jobs before resuming affected work, and never act on a
guessed or stale PID. A running worker keeps the constraints it was handed.
Tightening them means cancel and respawn, not a message sent mid-flight.

## Integrate and verify

As jobs finish:

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

   Then reopen the evidence yourself (`magus query output <ref>`). Wait proves the ref
   names a passing run of this job's own check, never that the work meets the job's
   GOAL. A worker's prose about its criteria is not that evidence.
3. Resolve cross-job API changes centrally; never assign the same seam twice.
4. Regenerate declared outputs once, after source work converges.
5. Re-run `magus affected <target> --plan` over the actual diff. If its shape
   invalidates the original partition, stop parallel integration and reconcile.
6. Read the integrated changeset with `magus diff --impact` before landing it. It shows reach, other recent
   editors, a rebuild estimate, advisors and anchored notes. It is context, never a
   verdict. An empty section means nobody could measure it, not that nothing was found.
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

Hand every spawn a job row forked first, read-only scouts included. A child bound to
no row is graded as you are, and no lease bounds you. "Fork the job, then spawn the
worker" below has the commands.

Fan-out is not inherently expensive. What costs is unbounded fan-out:
workers that hand work on without a shrinking scope, jobs with no acceptance criteria
so nobody can say when to stop, and a principal model assigned to mechanical edits.
Each of those is a choice made below, not a property of fanning out. Say what a
round costs when the user is deciding, and prefer the smallest fan-out that covers
the work.

Fan out only after the collision check below REPORTS the jobs disjoint; looking
separate is not that check. With only one coherent write set, keep the work local.
The root agent owns the goal, the budget, the topology, integration, and final
verification, and never hands those out.

## Coalesce what the write sets allow

Disjoint write sets LICENSE parallelism; they do not require it. Every worker carries
a fixed load before it reads a line of the diff: a system
prompt, the repository's instruction file, the routing index, the skills that
load, and the graph queries it runs to find its own footing, spent identically
whether the unit is fifty lines or five hundred. So after partitioning, ask whether
each unit is big enough to be worth a worker.

Apply these four in the order they bite:

- A depends-on chain is ONE worker in sequence, not two workers in turn.
  Two is two loads for one unit of work, and the second starts by rediscovering
  what the first just learned.
- Merge small disjoint units inside one project. Splitting them usually gains less
  wall clock than the load you pay twice, and
  they contend on the same validation anyway.
- Spawn only when more than one independent BIG unit survives the merge. One unit is
  inline work: a brief longer than the diff it asks for is the
  tell.
- Fill idle root time from that pool, never by splitting finer. A root blocked on a
  gate is a reason to start the next merged unit, and cutting a unit
  in half to have something to spawn buys wall clock with two fixed loads.

## Run the graph-engineering loop

Prove it before you plan it. A claim a plan or brief rests on cites the output ref of
a run that settled it. A guess nobody ran stays out of the plan.

Graph engineering is a natural evolution of loop engineering. The
human supplies a goal and constraints; the root agent turns them into explicit
acceptance criteria, uses the knowledge graph to partition the work, hands out
bounded prompts, observes results, evaluates them against the criteria, and
course-corrects until the integrated goal is satisfied. The graph improves the
loop's partition and collision decisions; it does not replace the loop.

Run this control loop:

1. State the top-level goal, constraints, and observable acceptance criteria.
2. Map the affected graph and propose collision-resistant edit jobs.
3. Give every job its own criteria, ownership boundary, and goals.
4. Hand out work, within any cap the user set.
5. Observe agents and Magus processes through their separate control planes.
6. Evaluate evidence, revise ownership or ordering when assumptions change, and
   repeat until the criteria pass.
7. Integrate centrally and run the release gate.

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
function. Name:

- the path;
- every exported name with its signature;
- the EXISTING symbols it must reuse, not restate.

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

Check each declared name with `magus refs` before forking. A bare name that resolves
to more than one definition is graded against one of them, silently.

This applies with zero workers. A solo change across several call sites is a one-job
tree. Skipping the declaration because nothing is forked is the same failure without
a fork to blame. Write the table in the
conversation, check it with `magus refs` when the edits land, and only then report
the change done.

## Set one topology boundary

Before spawning, state the topology: the model per job, and whether isolated
worktrees are available.

- Fan-out and depth are not capped unless the workspace sets a cap. Spawn as many
  jobs, nested as deep, as the partition supports.
- A limit exists only when magus.yaml's `jobs` section sets one. Read it with
  `magus config view` or `config`.
  - `max_depth` and `max_live` make `magus job fork` refuse past them, naming the key.
  - `default_timeout` bounds a fork that names no `--timeout`.
  - Honor a cap the user states the same way.
- Editing costs the workspace nothing; VALIDATION contends: the magus runs a job
  triggers. Read the live pool (`magus status`) before starting a validation, not
  before starting an agent. Serialize validations that share a worktree, even when
  their write sets are disjoint.

Assign the check from the pipeline the workspace composed, not from convention.
`magus describe target ci <project>` names what `ci` chains and in what order.

- A job gets the narrowest target from that decomposition.
- The integrator re-runs the described order, and `magus affected ci` re-proves the
  whole composition. A worker hand-sequencing lint, format, and test is re-deriving an
  order the magusfile already owns, and the step it forgets fails silently by
  omission.

A job's check is that narrow target, never the gate. The root gates ONCE, in its own
tree, after every unit lands, which is
what stops seven fanned-out workers from each running the whole pipeline
concurrently on one machine.

Decide each job's validation PLANE with its target. Some worker environments cannot
execute magus at all. Examples are an isolated tree with no usable binary, or a guard
routing raw tools to targets it cannot run. Such a worker cannot validate what it
writes. Mark that job's check ROOT-DEFERRED when you fork it:

- the worker writes the tests, stops at the static checks its environment does run,
  and says so;
- the root executes the job's target centrally before verifying. Leaving each worker to discover the wall
  spends its budget on the discovery, once per worker, and its report then reads
  "done" with nothing executed, which `magus job wait` already refuses to record
  as a pass.

A worker may fork part of its own job. It may not hand it out without shrinking the
problem; that is the shape that does not terminate, and
the cost people attribute to "multi-agent" is almost always this. These instructions give it a
definitive end without capping depth:

- **Every level narrows.** A child's scope is a strict subset of its parent's. A
  worker that would hand on its whole job does the work instead.
- **Every job carries acceptance criteria down.** A child inherits its parent's
  criteria plus its own. A job nobody can evaluate cannot end; that, not the nesting,
  makes depth dangerous.
- **A job that fails its criteria twice is not re-issued.** The root does it locally,
  or serializes it behind whatever keeps breaking it. Two jobs
  with an undeclared dependency each break the other's criteria, and re-issuing
  the failing one satisfies every instruction above while alternating forever; the budget
  is what ends it.
- **Whatever the parent does not hand out, the parent still owns.** A strict subset
  leaks by construction. Split "no caller of X remains" into per-project jobs, and
  the callers in no project belong to nobody. Every job passes and the goal is
  unmet. Carry a remainder row at each level and close it explicitly.

Pick the model that FITS the job, and SAY which one. The fit runs both ways. A
mechanical rename does not need the strongest model. An ambiguous API boundary
does not get the cheapest one because it looked like less work. Matching the model to the work is the only cost decision worth making here;
past that, cost is not your call to agonize over, and a job done badly by an
under-powered worker costs more than the model it saved.

Naming it is the checkable half: every spawn names a model, or an agent definition
that names one.

- Inheriting the parent's model ON PURPOSE is fine and often right. A hard review
  under a cheaper coordinator is exactly the case a weaker-only ordering would forbid.
- Inheriting it BY OMISSION is the failure. A host defaulting to "same as the parent"
  makes every unnamed spawn the most expensive one, and records no choice. Measured 2026-09-15: nine
  workers spawned in one session, every one inheriting the root's model, five of them
  mechanical work a cheaper model does as well.

ASK THE HUMAN when the right model is unclear, before spawning. There is no ordering to fall back on: "only ever spawn something
weaker" was tried and withdrawn, because same-strength offload is legitimate. An
unclear case is a question, not a default.

Model NAMES belong to the host, never to magus. Name the model your host names, or an
agent definition the user owns. Where the host has a default-subagent setting,
setting it makes omission cheap instead of expensive.

Map work to provider capabilities without assuming model names:

| Model | Assign |
|---|---|
| principal | architecture, ambiguous ownership, public APIs, migrations, security, integration |
| standard | isolated implementation with a clear contract and a bounded set of projects |
| economy | mechanical edits, fixtures, docs, inventory, and read-only evidence gathering |

If the host cannot select models or reasoning effort, keep its default. Tool access
is a separate axis: evidence gathering, scouting, and review get read-only tools
where the host offers them. Never downgrade the root integration pass or final
release gate.

Nesting is allowed when the host supports it, but it creates no new budget and no
private ownership map.

- Before a child spawns descendants, it reports the proposed jobs to its parent.
- The store then carries those descendants, their parent, model, criteria, and write
  paths.
- Descendants inherit the ancestor's deny paths and may subdivide only the ancestor's
  write paths.
- A cap the user sets applies to the whole tree, not once per parent.

Keep one integration owner at the root, however deep the job tree.
A child may coordinate its descendants, but it may not accept changes
outside its own job, relax top-level acceptance criteria, or hide additional
fan-out from the root. Nest wherever a child has a genuinely separable area and
enough context to partition it better than its parent.

## Seed the partition with magus

Choose the target that will validate the work. `ci` is the release gate, but any
target `magus affected <target>` accepts can be planned:

```sh
magus affected <target> --plan
```

For a proposed change whose paths are known but not yet edited, plan those paths
instead of the current diff:

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
- Use `magus refs <symbol>` when two jobs may touch the same API; `magus path <a> <b>` settles
  a suspicious pair.

A read-only job has no write set, so it is outside this analysis.

Two jobs may run together only when:

- The combined classification reports no overlaps: source write sets and declared
  outputs disjoint.
- Neither consumes an API or generated artifact the other changes.
- Shared manifests, lockfiles, schemas, workspace configuration, and agent
  instructions have one owner.
- Dependency and temporal-affinity evidence does not say they should move together.

ONE WORKTREE PER WORKER wherever a write path touches workspace configuration: any
project's magusfile, its `magus.yaml`, or a spell source a magusfile imports.

magus READS those files to load the workspace. A half-saved one stops the workspace
loading for everybody in that checkout at once. The rest lose `magus run`, `magus ls` and their own tests over an edit they
cannot see. Measured 2026-09-17: three workers
shared a checkout, one saved the root magusfile mid-edit, and all three were
stopped for the duration by a file only one of them had ever opened.
When `magus job fork` refuses one, the answer is a separate worktree, not a narrower
boundary.

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
   magus job fork api-store --model <model> \
     --criteria "accounts move to the new store; nothing names the old one" \
     --write-paths 'api/store.go,api/store_test.go,db/migrations/*.sql' \
     --check "go-test api -- -run TestStore" \
     --checkpoint <checkpoint>
   ```

   Or fork from a record with `magus job fork --stdin`, the form that carries goals
   (see the next section). For example:

   ```sh
   magus job fork --stdin <<'EOF'
   {"schema_version": 11, "id": "api-store/migrate", "parent": "api-store", "model": "<model>",
    "criteria": "accounts move to the new store; nothing names the old one",
    "write_paths": ["db/migrations/*.sql", "api/migrate.go"],
    "check": {"target": "go-test", "project": "api", "args": ["-run", "TestMigrate"]},
    "goals": [
      {"id": "migration", "kind": "paths", "expect": "changed", "paths": ["db/migrations/*.sql"]},
      {"id": "store", "kind": "symbol", "expect": "present", "symbols": ["AccountStore"]},
      {"id": "gone", "kind": "symbol", "expect": "absent", "symbols": ["LegacyAccountStore"]}
    ]}
   EOF
   ```

   `magus job fork --schema` prints every field and the newest `schema_version`.
3. Spawn the worker with the description `<parent>/<role> <job>`: two words, the
   first a parent id and a role joined by `/`, the second the job id.
   - A root job has no parent row, so its first word takes any label
     (`orchestrator/feat api-store`).
   - A child reads `api-store/fix migrate`; the guard resolves the job as `migrate`,
     else `api-store/migrate`.
   - The role is a word for the work (`feat`, `fix`, `review`). A workspace spawn
     rule may restrict it, and its refusal names the list.

Write paths name FILES: a file, a file the job creates, or a file glob
(`internal/queue/*.go`, `docs/**/*.md`). A directory, a project root, or a glob that
matches one (`api`, `internal/**`) is refused with [MGS3018](https://eli.gladman.cc/magus/reference/codes/sandbox/MGS3018/), because it
claims every file under it and so overlaps every job editing anything there.

Two jobs share one file by claiming declarations in it: `<file>#<declaration>`, such
as `internal/agent/catalog.go#SkillVersion`.

- A claim names one file, never a glob.
- The file needs a diff driver (`magus doctor` lists the managed ones); otherwise
  fork refuses with [MGS3031](https://eli.gladman.cc/magus/reference/codes/sandbox/MGS3031/).
- Two jobs that must edit the SAME declaration still have one owner. Give it to one
  job, or order them with `--depends-on`. Claiming the whole file, or a glob over its directory, is
  what serializes every other job that needed one function in it.

The check is `<target> <project> [-- args]` with the `magus run` implied (`"test ."`,
`"go-test api -- -run TestStore"`), never the gate or a target that chains to it.

Fork with `client` (`magus\job.put`) from an agent, or `magus job fork` from a
terminal: the same store and the same authorization either way, so a
job forked by hand and one an agent forked are indistinguishable to everything that
reads them. A worker holding a lease forks its own units the same way, naming its
job with `--parent` and paths inside its own.

The same checkpoint is what a later incremental re-review diffs from (see the
magus-change-summary skill); review time and pickup time read the same object.

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
commands as written. A line that names a command to forbid it ("never ...") is not
graded.

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
- run only its assigned magus target.

End every brief with the step that files the result, as the worker's LAST act before
it reports back. A brief that leaves it out gets a prose report and a row that never
moves: the store keeps the write paths held, `magus job wait`
has nothing to verify, and nobody notices until a sibling is refused over a path
the finished worker no longer needs. The step, with the shape `magus job exit --schema`
prints:

```sh
magus job exit <job> --stdin <<'EOF'
{"schema_version": 2, "job": "<job>",
 "changed_paths": ["api/store.go", "api/store_test.go"],
 "validation": {"command": "magus run go-test api -- -run TestStore", "output_ref": "<ref>"},
 "descendants": [],
 "unresolved_risks": ["what is left, or what the worker could not verify"]}
EOF
```

A typed result makes verification mechanical; the same four facts in prose can only be
graded by reading, and a worker that ran a filtered subset writes the same
paragraph as one that did not. Keep unresolved risks mandatory, so a mis-scoped
worker can say so instead of widening silently.

A worker writes only its own row and the children it forks. Every other store write
is the orchestrator's; a refused worker reports it as an unresolved risk and stops.

The checkpoint you recorded is what you HANDED the job; the base it LANDED ON is a
separate fact. Hosts that isolate workers in per-worker trees routinely branch them
from an older revision than the tree you partitioned, and every diff-since-checkpoint in Integrate and verify
silently lies when the recorded base is not the real one.

- A worker whose spawn title names its job (`<parent>/<role> <job>`) is bound and
  records its base on its first call. Any other worker runs `magus job exec <its id>`
  once to record it.
- The guard binds the caller that ran it, keyed on the host's session and subagent
  ids. Workers sharing a tree each hold their own lease, and none binds you. Nothing to pass: the
  CLI cannot tell a subagent from its parent, which is why the binding is the
  guard's, and a host that names neither id binds the checkout for every caller like
  it.
- The answer is a status on the row: match, revision-match (same revision, different
  uncommitted patch), diverged, or unknown. A reading names both tokens and the next
  step.
- It is a FACT, not a gate: every status records, diverged included, because refusing would leave the orchestrator
  with no record that a worker went to the wrong base, which is the one case the
  record exists for.
- Acting on it is yours. Respawn from the right revision. Or have the worker
  materialize the files it builds on from the intended one, and re-fork the job
  with the right checkpoint. Materialize with `git show <rev>:<path> > <path>`, verifying
  each blob against `git rev-parse <rev>:<path>`. A worker
  that edits stale content without noticing reports clean validation against a tree
  nobody ever merges.

Name any fact that will READ as drift to the worker's snapshot (a project deleted
this session, a rename, an index regenerated underneath it). Never write a generic
"expect drift" line, which only primes the worker to dismiss real
anomalies: the specific fact is what keeps unexplained tree state from costing
an investigation or a helpful revert of something correct.

Write paths bound READS too: a worker may read its projects and what they declare
`depends_on`. When a worker must READ something it must not WRITE, put that path in the
row's `read_paths` instead of widening `write_paths`: one list cannot say
both, and widening the write paths to open a read is how two workers end up owning
one file.

Ownership ends when EDITING ends, not when the worker exits. A worker done writing a
contested path announces the release at once, then carries on validating. It shrinks
the job's `write_paths` with another `client` (`magus\job.put`) write, or messages
the orchestrator if the host supports it. A waiting job
starts against the released file while the first is still running tests, which
is most of a worker's lifetime; holding every path to exit serializes agents on
time they spend not editing.

That write records each dropped path with its digest at that moment. Hand the digest
to the job taking the path over. One that no longer matches at verification means the
waiter built on a tree the releaser never saw.

Moving a live job's boundary is yours. Change its record and run `magus job apply -f
<file>` (`-f -` reads stdin).

- The record is the whole spec: one write widens or revokes write paths and adds
  goals, and the job keeps its state. A re-fork would hand a taken job out again as
  declared.
- `--dry-run` prints the spec diff and writes nothing.
- A check you find the job owes mid-flight is a goal you add this way, so `magus job
  wait` grades it from a recorded run.
- A revoked path is recorded as a release with its digest. The worker's next write
  there is refused, naming the revocation.
- Ending a whole job stays `magus job exit <job>`. A worker never widens: its refusal
  names `magus describe job` and tells it to ask you.

Advance the row on every state change. `magus ls jobs` then shows which live jobs
claim intersecting `write_paths` and how long since each row was touched. A reported
overlap is a pair you either intended or must repartition.

magus ENDS a live job itself, as `no_return` with an `end_reason`, on every read of the
store when it can prove nobody holds it:

- an ancestor ended;
- the checkout `magus job exec` took it in no longer exists;
- it is still `declared`, nobody ever took it, and it was not updated within
  `jobs.stale_after` (default 2h, `0` for never). A root outlives children still working under it;
  the guard noting somebody else's write in a job's paths does not count as an
  update.

Each ended job prints `ended <id>: <reason>` on stderr. So remove a worker's worktree
only once its job is done, and advance a root you are still using. What magus cannot
prove it leaves live, and ending those stays yours. A taken job that went quiet reads
`stale`, one past its timeout `overdue`, each with `magus job exit <id>`.

`--timeout <duration>` on fork is OPTIONAL and unset by default. Past it the guard
denies every write graded under that lease, and its paths stop blocking other jobs.
The row stays live until you end it. A job with acceptance criteria needs no bound: the goals end it, and a timeout only helps where a stuck worker
would otherwise hold paths nobody else can write.

`magus job wait` holds a writing job's `changed_paths` to the diff magus observes since
its checkpoint:

- Every claimed path must be in that diff, and something in it must be inside the
  write paths.
- A diff it cannot read FAILS, so fork writing jobs with a checkpoint.
- It refuses pass while any descendant is still live.
- It grades a child against its own symbol goals and its ancestors'.

The guard grades each write of a worker that exported `magus.lease` against these rows
and denies one outside them, naming the owner. A writer magus cannot attribute is only
ADVISED, and every uncertainty fails open: a seatbelt, not a sandbox.

So a denied worker COORDINATES and never works around. Ask the orchestrator to
re-partition, or have the owning job release the path, then retry. Editing
anyway from an un-enrolled shell, or dropping the lease id to buy advisory
treatment, turns a denial you could have acted on into a collision nobody sees
until integration. Step 1 of Integrate and verify checks the same boundary against
the checkpoint: the half that does not depend on a worker cooperating.

A read-only job carries an abbreviated row: no write paths, no deny paths. Every row
ends in pass, fail, or NO-RETURN, and the root writes which: silence
is not a pass, and a worker that dies, stalls, or is killed is a different state
from one that failed its criteria.

Acceptance criteria must be observable. Prefer named tests, generated
artifacts, diagnostics, API behavior, or specific review checks over phrases such
as "works correctly." A child that hands work on remains responsible for evaluating
its descendants before reporting upward. The root still verifies the combined
result independently.

## Declare the criteria magus can check for you

A job's acceptance criteria are prose a reader grades. A GOAL is the part magus grades
itself, from evidence the worker cannot author. `magus job wait` refuses to record pass
until every one verifies.

- Goals are data: write them in the job record's `goals`, never as flags.
- `magus job fork` refuses a job that writes and declares neither a check nor a goal.
- `client` (`magus\job.put`) takes the same `goals` array.
- The `--stdin` record above declares a `paths` goal and two `symbol` goals.

A goal names WHAT it examines and what must be true of it:

| kind     | expects                                         | read from                                      |
| -------- | ----------------------------------------------- | ---------------------------------------------- |
| `check`  | `passed`                                        | a recorded run, captured after the declaration |
| `paths`  | `changed`, `present`, `absent`                  | the diff since the checkpoint, or the tree now |
| `symbol` | `changed`, `present`, `absent`, `unreferenced`  | the knowledge graph, below file granularity    |

One entry reads `{"id": "gone", "kind": "symbol", "expect": "absent", "symbols": ["LegacyAccountStore"]}`.
Each kind has a default expectation (`passed` for a check, `changed` otherwise), so the
common goal names only its kind and subject.

Reach for `symbol` + `unreferenced` when partitioning a rename: the REMAINDER instruction made
checkable. Split per project, and the callers in no project belong to no job. Every
job passes and the rename is unfinished.

- Grade it while the old name is still defined. The graph counts references into a
  definition; once the definition is gone, `unreferenced` FAILS rather than guess.
- The job that deletes it declares `absent` beside a check that builds the callers.

Every kind reads what magus already holds, which makes a goal a contract, not an
attestation. There is no goal for "this command exited 0", deliberately: magus
did not record that run and cannot attribute it, so it would be the easiest goal to
satisfy falsely. Declare a target and use `check`.

The worker's own `changed_paths` is its account, never the evidence. The diff and the
tree are read in the checkout that took the job, so wait from your own tree while the
worker's still exists. An observation magus could not MAKE fails the goal: otherwise the cheapest way past
a goal would be to break the observation.

Ask where a job stands without advancing it:

```sh
magus describe job <job>
```

It prints where each goal stands beside the terms: the same grading `magus job wait`
does, recording nothing. Use it instead of asking a worker how it is going.

SEQUENCE goals with `depends_on` between them; a failed prerequisite propagates. Do
NOT nest them: a goal that wants children is a JOB that wants splitting, which the instructions above cover. Flat goals keep
the job tree the only hierarchy with an owner.

Run workers non-blocking by default. Block on one only when your next action needs its
result. An agent spawned only to wait, poll, or repeat the root's discovery is not an
edit job and spends budget for nothing.

## Observe through the correct control plane

Track the job tree, agent state, messages, and completion in the provider's agent or
task view. Use it to keep the store aware of descendants.

Watch processes and shared workspace resources with magus:

```sh
magus status --watch=15s
```

It shows magus process state, lock holders, and shared-service state and adoption. It
does not show an agent thinking without running a magus process. Never replace it with
sleep loops, repeated `ps`, or a waiting agent.

To wait for a process you did not start to end, use your host's own wait or monitor
tool: a shell loop holds your tool slot for the whole wait.

### "How is it going" is a read, never a message

Never message a worker to ask how it is doing. The question costs it the turn it was
in, and what comes back is its account of itself, not what happened. Three reads answer it and none of them needs the
worker's cooperation: a changed file is the filesystem reporting a fact, a tool
call is what the guard already recorded, and a gate is graded against evidence
magus is holding anyway.

| you want | read |
| --- | --- |
| to hand the question to a person | the console link every verb that names a job prints |
| to watch it happen | `magus job watch <job>` |
| to know whether it is finished | `magus describe job <job>` |

`magus job watch` prints one line per event until you interrupt it. It merges files
changed under the job's write paths, the guard's tool calls under its lease, and the
runs recorded against it. A file is attributed by WRITE PATH and by nothing the worker says: the write paths are proven disjoint when the job forks, so the
path alone names the holder. Where two live jobs do cover one path the line says
`contested`, names both, and attributes it to neither; there is nothing in a path
to break that tie with, and naming one would tell you a file moved under a worker
that never touched it.

Message a worker only to CHANGE what it was handed. Anything you merely want to KNOW
is one of the three reads above.

A blocked worker RAISES; it never stalls quietly.

- Piping the block to `magus session notify --outcome waiting` (blocked on input) or
  `--outcome permission` (blocked on approval) opens a durable request in this
  repository. No other outcome opens one.
- `magus session attention` lists what is open, keyed by repository identity rather
  than by checkout path, so a request raised inside a worker's own isolated tree is
  listed in yours. `magus session attention -q` prints nothing and exits 1 on an empty
  queue: the form to test from a loop.
- Nothing closes a request by itself. The orchestrator, or any human, disposes it
  with `magus session dispose <id> --reason "<why>"`. There is no expiry and no auto-dispose, because a
  request magus could answer on its own would not have needed a person.
- A worker that raised one waits for the disposition; it never chooses for itself.

`magus session` is how the root audits what a job RAN, as opposed to what it reported.
`magus session --since 2h -o json` answers what the fleet has been doing.

Each invocation carries the job it was launched under (the same `magus.lease` channel).
It also carries its claimed spawner label and parent span, and the targets it finished
with their outcomes. The store is keyed by repository identity, so a worker in its own
worktree is still listed.
Attribution is cooperative and every one of those values is a CLAIM magus records
rather than corroborates: an empty lease means the invocation claimed none, which
makes it unattributed, never an error; its OS user says whose account ran it.

Course-correct at explicit checkpoints: after a child proposes new
descendants, when a worker discovers a new API or generated-output dependency,
when ownership drifts, when criteria repeatedly fail, and when status shows
unexpected lock contention or service failure. Pause only the affected branch,
update the jobs and ordering, then resume work that remains independent. Never
guess a PID or signal from stale output; use current status and the host's normal
process controls. A running worker keeps the constraints it was handed.
Tightening them means cancel and respawn, not a message sent mid-flight.

## Integrate and verify

As jobs finish:

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
4. Regenerate declared outputs once, after source work converges.
5. Re-run `magus affected <target> --plan` over the actual diff. If its shape
   invalidates the original partition, stop parallel integration and reconcile.
6. Read the integrated changeset with `magus diff --impact` before landing it. It shows
   what the fleet's combined edit reaches, who else has been changing it, and a
   rebuild estimate from recorded run times. It adds what the advisors say and any
   note anchored to a file it touched. Context, never a verdict: nothing gates on it and
   the exit code is unchanged, and a section that is empty means nobody could
   measure it, not that nothing was found.
7. Run `magus affected ci` and evaluate the top-level acceptance criteria.

Parallelism is an optimization, not the objective. Fewer
well-isolated jobs are usually cheaper than wide fan-out followed by conflict
repair, and the graph is evidence for that judgment rather than permission to
spawn every possible worker.
````


</section>

</article>
