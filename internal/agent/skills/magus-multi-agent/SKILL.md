# Splitting work across agents

{{if .Full}}Count the WRITE SETS your change needs (the distinct groups of files to edit), not the
projects it invalidates. Getting this backwards is the standard fan-out failure: a
one-line edit in a central package invalidates half the workspace and is still one
edit, and fanning it out produces several agents editing one file.{{else}}Count WRITE SETS change needs (distinct groups of files to edit), not projects it invalidates; backwards count is standard fan-out failure.
One-line edit in central package invalidates half workspace, still one edit.{{end}}

{{if .Full}}`{{cmd "affected"}} <target> --plan` partitions VALIDATION: which targets to run, grouped
for runner balance. It is neither an edit assignment nor a proof of write isolation.
So it can veto a fan-out and never license one. One shard means keep the work local.
Several mean the testing parallelizes, and the editing is still your question.{{else}}`{{cmd "affected"}} <target> --plan` partitions VALIDATION: which targets to run, grouped for runner balance.
Neither edit assignment nor proof of write isolation -> can veto fan-out, never license one.
One shard: keep work local. Several: testing parallelizes; editing still your question.{{end}}

{{if .Full}}Before any edits exist, the shard plan is empty and proves nothing. Finding the
candidate paths IS the partitioning work, done with the graph, not by intuition:{{else}}Before any edits exist, shard plan empty, proves nothing.
Finding candidate paths IS partitioning work, done with graph:{{end}}

```sh
magus graph build                           # first in a fresh worktree, or refs answers "unknown, not absent"
magus refs <symbol> --occurrences -o json   # every edit site, column-precise
magus explain <node>                        # one node's edges and blast radius
magus affected ci --plan --stdin            # plan PROPOSED paths, before editing
```

{{if .Full}}You do not need to be asked to split work. "Split this across agents" is one way in;
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
verification, and never hands those out.{{else}}No need to be asked to split: several disjoint write sets you can name are reason enough.
Every spawn gets job row forked first, read-only scouts included; child bound to no row is graded as you are, and no lease bounds you. Commands: "Fork job, then spawn worker" below.
Fan-out not inherently expensive. Say round cost when user is deciding; prefer smallest fan-out covering work.
Fan out only after collision check below REPORTS jobs disjoint; looking separate is not that check.
Only one coherent write set -> keep work local.
Root agent owns goal, budget, topology, integration, final verification; never hands those out.{{end}}

{{if .Full}}## Coalesce what the write sets allow{{else}}## Coalesce what write sets allow{{end}}

{{if .Full}}Disjoint write sets LICENSE parallelism; they do not require it. Every worker carries
a fixed load before it reads a line of the diff: a system
prompt, the repository's instruction file, the routing index, the skills that
load, and the graph queries it runs to find its own footing, spent identically
whether the unit is fifty lines or five hundred. So after partitioning, ask whether
each unit is big enough to be worth a worker.

Apply these four in the order they bite:
{{else}}Disjoint write sets LICENSE parallelism; do not require it.
Every worker carries fixed load before reading a line of diff -> after partitioning, ask whether each unit big enough to be worth worker.
Apply these four in order they bite:{{end}}
{{if .Full}}- A depends-on chain is ONE worker in sequence, not two workers in turn.
  Two is two loads for one unit of work, and the second starts by rediscovering
  what the first just learned.{{else}}- Depends-on chain: ONE worker in sequence, not two workers in turn.{{end}}
{{if .Full}}- Merge small disjoint units inside one project. Splitting them usually gains less
  wall clock than the load you pay twice, and
  they contend on the same validation anyway.{{else}}- Merge small disjoint units inside one project: splitting usually gains less wall clock than load paid twice.{{end}}
{{if .Full}}- Spawn only when more than one independent BIG unit survives the merge. One unit is
  inline work: a brief longer than the diff it asks for is the
  tell.{{else}}- Spawn only when more than one independent BIG unit survives merge. One unit: inline work.{{end}}
{{if .Full}}- Fill idle root time from that pool, never by splitting finer. A root blocked on a
  gate is a reason to start the next merged unit, and cutting a unit
  in half to have something to spawn buys wall clock with two fixed loads.{{else}}- Fill idle root time from that pool, never by splitting finer. Root blocked on gate: reason to start next merged unit.{{end}}

{{if .Full}}## Run the graph-engineering loop{{else}}## Graph-engineering loop{{end}}
{{if .Full}}
Prove it before you plan it. A claim a plan or brief rests on cites the output ref of
a run that settled it. A guess nobody ran stays out of the plan.{{end}}{{if .Full}}

Graph engineering is a natural evolution of loop engineering: the graph improves
the loop's partition and collision decisions, and does not replace the loop. Run it:{{end}}{{if .Full}}

1. State the top-level goal, constraints, and observable acceptance criteria.
2. Map the affected graph and propose collision-resistant edit jobs.
3. Give every job its own criteria, ownership boundary, and goals.
4. Hand out work, within any cap the user set.
5. Observe agents and Magus processes through their separate control planes.
6. Evaluate evidence and revise ownership or ordering until the criteria pass.
7. Integrate centrally and run the release gate.
{{end}}
{{if .Full}}Acceptance evidence is an output ref the root reopens (`{{cmd "query output"}} <ref>`),
never a worker's prose. A worker that ran a filtered subset, or quietly restated its
criteria into something it did pass, reports success either way, and a
transcript cannot tell you which happened.

An agent may report that its edits are done, but no job is complete until its
acceptance criteria and assigned check pass. The root agent, not a worker,
decides whether the top-level goal is complete.{{else}}Prove before planning: claim a plan or brief rests on cites output ref of run that settled it. Guess nobody ran stays out of plan.
Loop: define, partition, hand out, observe, evaluate, course-correct, integrate.
- Acceptance evidence: output ref root reopens (`{{cmd "query output"}} <ref>`), never worker prose.
- Worker not complete until its criteria and assigned check pass.
- Root agent decides whether top-level goal complete.{{end}}

{{if .Full}}## Declare the interface before any job forks{{else}}## Declare interface before any job forks{{end}}

{{if .Full}}When a change adds a shared API, the ROOT names it before any edit. A shared
API is a module several call sites use, a type, an event, or an exported
function. Name:
{{else}}When change adds shared API (module several call sites use, type, event, exported function), ROOT names it before any edit:{{end}}
{{if .Full}}- the path;{{else}}- path;{{end}}
{{if .Full}}- every exported name with its signature;{{else}}- every exported name with signature;{{end}}
{{if .Full}}- the EXISTING symbols it must reuse, not restate.{{else}}- EXISTING symbols it must reuse, not restate.
Workers implement names handed to them, never coin public one; name invented at each call site -> one concept, five spellings.{{end}}{{if .Full}}

Workers implement the names they were handed and never coin a public one. A name
invented at each call site is how one concept ends up with five spellings, and how a plan that says "collapse two state types into one" ships
a third: each site made a locally sensible choice and nobody owned the set.{{end}}

{{if .Full}}Then make the declaration gradeable, so the names are a contract, not a suggestion:
{{else}}Make declaration gradeable -> names are contract, not suggestion:{{end}}
{{if .Full}}- `symbol` + `present` for each new exported name.{{else}}- `symbol` + `present`: each new exported name.{{end}}
{{if .Full}}- `symbol` + `absent` for the name a second copy would predictably take beside a
  symbol the work must reuse. No gate grades reuse itself: `present`
  holds for a symbol that existed before the job began, and a reference count
  cannot tell the defining file or an import from real use.{{else}}- `symbol` + `absent`: name a second copy would predictably take beside symbol work must reuse. No gate grades reuse itself.{{end}}
{{if .Full}}- `symbol` + `unreferenced` for each helper the shared API replaces.

Check each declared name with `{{cmd "refs"}}` before forking. A bare name that resolves
to more than one definition is graded against one of them, silently.

This applies with zero workers. A solo change across several call sites is a one-job
tree. Skipping the declaration because nothing is forked is the same failure without
a fork to blame. Write the table in the
conversation, check it with `{{cmd "refs"}}` when the edits land, and only then report
the change done.{{else}}- `symbol` + `unreferenced`: each helper shared API replaces.
Check each declared name with `{{cmd "refs"}}` before forking: bare name resolving to more than one definition graded against one of them, silently.
Applies with zero workers: solo change across several call sites is one-job tree; skipping declaration because nothing forked is same failure.{{end}}

{{if .Full}}## Set one topology boundary{{else}}## One topology boundary{{end}}

{{if .Full}}Before spawning, state the topology: the model per job, and whether isolated
worktrees are available.
{{else}}Before spawning, state topology: model per job, whether isolated worktrees available.{{end}}
{{if .Full}}- Fan-out and depth are not capped unless the workspace sets a cap. Spawn as many
  jobs, nested as deep, as the partition supports.{{else}}- Fan-out and depth not capped unless workspace sets cap; spawn as many jobs, nested as deep, as partition supports.{{end}}
{{if .Full}}- A limit exists only when magus.yaml's `jobs` section sets one. Read it with
  `{{cmd "config view"}}` or `{{tool "config"}}`.
  - `max_depth` and `max_live` make `{{cmd "job fork"}}` refuse past them, naming the key.
  - `default_timeout` bounds a fork that names no `--timeout`.
  - Honor a cap the user states the same way.{{else}}- Limit exists only when magus.yaml `jobs` section sets one; read with `{{cmd "config view"}}` or `{{tool "config"}}`.
  - `max_depth`, `max_live`: `{{cmd "job fork"}}` refuses past them, naming key.
  - `default_timeout` bounds fork naming no `--timeout`.
  - Honor cap user states same way.{{end}}
{{if .Full}}- Editing costs the workspace nothing; VALIDATION contends: the magus runs a job
  triggers. Read the live pool (`{{cmd "status"}}`) before starting a validation, not
  before starting an agent. Serialize validations that share a worktree, even when
  their write sets are disjoint.{{else}}- Editing costs workspace nothing; VALIDATION contends (magus runs a job triggers). Read live pool (`{{cmd "status"}}`) before starting validation, not before starting agent. Serialize validations sharing worktree, even when write sets disjoint.{{end}}

{{if .Full}}Assign the check from the pipeline the workspace composed, not from convention.
`{{cmd "describe target"}} ci <project>` names what `ci` chains and in what order.
{{else}}Assign check from pipeline workspace composed, not convention: `{{cmd "describe target"}} ci <project>` names what `ci` chains, in what order.{{end}}
{{if .Full}}- A job gets the narrowest target from that decomposition.{{else}}- Job gets narrowest target from that decomposition.{{end}}
{{if .Full}}- The integrator re-runs the described order, and `{{cmd "affected"}} ci` re-proves the
  whole composition. A worker hand-sequencing lint, format, and test is re-deriving an
  order the magusfile already owns, and the step it forgets fails silently by
  omission.

A job's check is that narrow target, never the gate. The root gates ONCE, in its own
tree, after every unit lands, which is
what stops seven fanned-out workers from each running the whole pipeline
concurrently on one machine.{{else}}- Integrator re-runs described order; `{{cmd "affected"}} ci` re-proves whole composition.
Job's check: that narrow target, never gate. Root gates ONCE, in own tree, after every unit lands.{{end}}

{{if .Full}}Decide each job's validation PLANE with its target. Some worker environments cannot
execute magus at all. Examples are an isolated tree with no usable binary, or a guard
routing raw tools to targets it cannot run. Such a worker cannot validate what it
writes. Mark that job's check ROOT-DEFERRED when you fork it:{{else}}Decide each job's validation PLANE with its target.
Some worker environments cannot execute magus at all (isolated tree with no usable binary; guard routing raw tools to targets it cannot run) -> worker cannot validate what it writes -> mark check ROOT-DEFERRED when forking:
- worker writes tests, stops at static checks its environment does run, says so;
- root executes job's target centrally before verifying.{{end}}{{if .Full}}

- the worker writes the tests, stops at the static checks its environment does run,
  and says so;
- the root executes the job's target centrally before verifying. Leaving each worker to discover the wall
  spends its budget on the discovery, once per worker, and its report then reads
  "done" with nothing executed, which `{{cmd "job wait"}}` already refuses to record
  as a pass.{{end}}{{if .Full}}

A worker may fork part of its own job. It may not hand it out without shrinking the
problem; that is the shape that does not terminate, and
the cost people attribute to "multi-agent" is almost always this. These instructions give it a
definitive end without capping depth:{{end}}

{{if .Full}}- **Every level narrows.** A child's scope is a strict subset of its parent's. A
  worker that would hand on its whole job does the work instead.{{else}}Worker may fork part of own job; may not hand it out without shrinking problem. Definitive end without capping depth:
- Every level narrows: child scope strict subset of parent's. Worker that would hand on whole job does work instead.{{end}}
{{if .Full}}- **Every job carries acceptance criteria down.** A child inherits its parent's
  criteria plus its own. A job nobody can evaluate cannot end; that, not the nesting,
  makes depth dangerous.{{else}}- Every job carries acceptance criteria down: child inherits parent's criteria plus own. Job nobody can evaluate cannot end; that, not nesting, makes depth dangerous.{{end}}
{{if .Full}}- **A job that fails its criteria twice is not re-issued.** The root does it locally,
  or serializes it behind whatever keeps breaking it. Two jobs
  with an undeclared dependency each break the other's criteria, and re-issuing
  the failing one satisfies every instruction above while alternating forever; the budget
  is what ends it.{{else}}- Job failing criteria twice: not re-issued. Root does it locally, or serializes it behind whatever keeps breaking it.{{end}}
{{if .Full}}- **Whatever the parent does not hand out, the parent still owns.** A strict subset
  leaks by construction. Split "no caller of X remains" into per-project jobs, and
  the callers in no project belong to nobody. Every job passes and the goal is
  unmet. Carry a remainder row at each level and close it explicitly.{{else}}- Whatever parent does not hand out, parent still owns. Strict subset leaks by construction: split "no caller of X remains" into per-project jobs -> callers in no project belong to nobody; every job passes, goal unmet. Carry remainder row at each level, close explicitly.{{end}}

{{if .Full}}START EVERY SPAWN AT THE ECONOMY TIER, and SAY which model you picked. Move a job up
only for work the table below assigns higher. The fit still runs both ways: an
ambiguous API boundary does not get the cheapest model because it looked like less
work. Judge cost per COMPLETED job: a cheap worker that needs a rerun is not
cheaper.

Naming it is the checkable half: every spawn names a model, or an agent definition
that names one.
{{else}}Start every spawn at economy tier, and SAY which model picked.
Move job up only for work list below assigns higher.
Fit runs both ways: ambiguous API boundary does not get cheapest model because it looked like less work.
Checkable half: every spawn names model, or agent definition naming one.{{end}}
{{if .Full}}- Inheriting the parent's model ON PURPOSE is fine and often right. A hard review
  under a cheaper coordinator is exactly the case a weaker-only ordering would forbid.{{else}}- Inheriting parent's model ON PURPOSE: fine, often right.{{end}}
{{if .Full}}- Inheriting it BY OMISSION is the failure. A host defaulting to "same as the parent"
  makes every unnamed spawn the most expensive one, and records no choice. Measured 2026-09-15: nine
  workers spawned in one session, every one inheriting the root's model, five of them
  mechanical work a cheaper model does as well.{{else}}- Inheriting BY OMISSION: failure; host defaulting to "same as the parent" makes every unnamed spawn most expensive, records no choice.
When tier unclear -> start at economy. Job failing criteria there: re-forked one tier up, not retried at same.
Model NAMES belong to host, never magus: name host's model or agent definition user owns. Point default-subagent setting at economy model.{{end}}{{if .Full}}

When the tier is unclear, start at economy. A job that fails its criteria there is
re-forked one tier up, not retried at the same one. "Only ever spawn something
weaker" was tried and withdrawn, because same-strength offload is legitimate, so this
is a starting point and not a ceiling.{{end}}{{if .Full}}

Model NAMES belong to the host, never to magus: name your host's model or an agent
definition the user owns. Point a default-subagent setting at the economy model.{{end}}

Map work to provider capabilities without assuming model names:
{{if .Full}}
| Model | Assign |
|---|---|
| principal | architecture, ambiguous ownership, public APIs, migrations, security, integration |
| standard | isolated implementation with a clear contract and a bounded set of projects |
| economy | mechanical edits, fixtures, docs, inventory, and read-only evidence gathering |{{else}}- principal: architecture, ambiguous ownership, public APIs, migrations, security, integration
- standard: isolated implementation with clear contract, bounded set of projects
- economy: mechanical edits, fixtures, docs, inventory, read-only evidence gathering{{end}}

{{if .Full}}If the host cannot select models or reasoning effort, keep its default. Tool access
is a separate axis: evidence gathering, scouting, and review get read-only tools
where the host offers them. Never downgrade the root integration pass or final
release gate.{{else}}If host cannot select models or reasoning effort -> keep default.
Tool access separate axis: evidence gathering, scouting, review get read-only tools where host offers them.
Never downgrade root integration pass or final release gate.{{end}}

{{if .Full}}To prove something, brute-force it at economy. Many economy workers finish about as
fast as one principal worker, for far fewer tokens:
{{else}}To prove something, brute-force at economy: many economy workers finish about as fast as one principal, far fewer tokens:{{end}}
{{if .Full}}- Fork one read-only job per claim, input, or variant, and run them all at once.
  Give each a script check, so `{{cmd "job wait"}}` can pass it.{{else}}- Fork one read-only job per claim, input, or variant; run all at once. Each gets script check -> `{{cmd "job wait"}}` can pass it.{{end}}
{{if .Full}}- Spawn the `magus-scout` agent for each where your harness installed it.{{else}}- Spawn `magus-scout` agent for each where harness installed it.{{end}}
{{if .Full}}- Each reports its command and output ref, so evidence settles the claim and not a worker's reading.{{else}}- Each reports command and output ref.{{end}}
{{if .Full}}- The principal tier only reconciles the claims where workers disagree.{{else}}- Principal tier only reconciles claims where workers disagree.{{end}}

Keep economy jobs short-lived:
{{if .Full}}
- The brief points at evidence (paths, output refs, the row) and carries no transcript.{{else}}- Brief points at evidence (paths, output refs, row); carries no transcript.{{end}}
{{if .Full}}- A worker resends its whole context every turn, and some providers raise a tier's
  price past a prompt size. Where the brief names that size, the worker reports and
  stops before passing it.{{else}}- Worker resends whole context every turn; some providers raise tier price past a prompt size. Where brief names that size, worker reports and stops before passing it.{{end}}

{{if .Full}}Nesting is allowed when the host supports it, but it creates no new budget and no
private ownership map.
{{else}}Nesting is allowed when the host supports it; creates no new budget, no private ownership map.{{end}}
{{if .Full}}- Before a child spawns descendants, it reports the proposed jobs to its parent.{{else}}- Before child spawns descendants, it reports proposed jobs to parent.{{end}}
{{if .Full}}- The store then carries those descendants, their parent, model, criteria, and write
  paths.{{else}}- Store then carries descendants, parent, model, criteria, write paths.{{end}}
{{if .Full}}- Descendants inherit the ancestor's deny paths and may subdivide only the ancestor's
  write paths.{{else}}- Descendants inherit ancestor's deny paths; may subdivide only ancestor's write paths.{{end}}
{{if .Full}}- A cap the user sets applies to the whole tree, not once per parent.{{else}}- Cap user sets applies to whole tree, not once per parent.
One integration owner at root, however deep job tree.
Child coordinates descendants; may not relax root's criteria.{{end}}{{if .Full}}

Keep one integration owner at the root, however deep the job tree.
A child may coordinate its descendants, but it may not accept changes
outside its own job, relax top-level acceptance criteria, or hide additional
fan-out from the root. Nest wherever a child has a genuinely separable area and
enough context to partition it better than its parent.{{end}}

{{if .Full}}## Seed the partition with magus{{else}}## Seed partition with magus{{end}}

{{if .Full}}Choose the target that will validate the work. `ci` is the release gate, but any
target `{{cmd "affected"}} <target>` accepts can be planned:{{else}}Choose target that validates work. `ci`: release gate, but any target `{{cmd "affected"}} <target>` accepts can be planned:{{end}}

```sh
magus affected <target> --plan
```

{{if .Full}}For a proposed change whose paths are known but not yet edited, plan those paths
instead of the current diff:{{else}}Proposed change, paths known but not yet edited -> plan those paths, not current diff:{{end}}

```sh
printf '%s\n' <repo-relative-path>... | magus affected <target> --stdin --plan
```

{{if .Full}}Read the JSON fields `count`, `max_parallel`, `source`, and `matrix`. A shard is a
history-balanced execution group, not an edit assignment or proof of write isolation.
Use a shard only as the first partition. If paths are unknown, query the task's symbols,
files, and projects before producing a stdin plan.{{else}}Read JSON fields `count`, `max_parallel`, `source`, `matrix`.
Use shard only as first partition.
If paths unknown -> query task's symbols, files, projects before producing stdin plan.{{end}}

{{if .Full}}## Prove that jobs do not collide{{else}}## Prove jobs do not collide{{end}}

{{if .Full}}Classify the union of every job's proposed paths in one call:{{else}}Classify union of every job's proposed paths in one call:{{end}}

```sh
magus describe file <both jobs' paths>... -o json
```
{{if .Full}}
Read the facts:
{{end}}
{{if .Full}}- `overlaps` lists each declaration covering more than one proposed path: a shared
  write set by construction.{{else}}Facts:
- `overlaps`: each declaration covering more than one proposed path: shared write set by construction.{{end}}
{{if .Full}}- `claims[].target` names the target that regenerates a path. Generated outputs have
  one integration owner and are never hand-edited by workers.{{else}}- `claims[].target`: target regenerating path. Generated outputs: one integration owner; never hand-edited by workers.{{end}}
{{if .Full}}- `depends_on` carries the owner's direct edges.{{else}}- `depends_on`: owner's direct edges.{{end}}
{{if .Full}}- Affinity stays with `{{tool "client"}}` (`{{buzz "insight"}}`, read affinity). Without MCP,
  run `{{cmd "buzz"}} -e` printing `magus\insight().affinity`.{{else}}- Affinity stays with `{{tool "client"}}` (`{{buzz "insight"}}`, read affinity). Without MCP: `{{cmd "buzz"}} -e` printing `{{buzz "insight"}}().affinity`.{{end}}
{{if .Full}}- Use `{{cmd "refs"}} <symbol>` when two jobs may touch the same API; `{{cmd "path"}} <a> <b>` settles
  a suspicious pair.

A read-only job has no write set, so it is outside this analysis.{{else}}- `{{cmd "refs"}} <symbol>` when two jobs may touch same API.
Read-only job: no write set -> outside this analysis.{{end}}

Two jobs may run together only when:
{{if .Full}}
- The combined classification reports no overlaps: source write sets and declared
  outputs disjoint.{{else}}- combined classification reports no overlaps: source write sets and declared outputs disjoint;{{end}}
{{if .Full}}- Neither consumes an API or generated artifact the other changes.{{else}}- neither consumes API or generated artifact other changes;{{end}}
{{if .Full}}- Shared manifests, lockfiles, schemas, workspace configuration, and agent
  instructions have one owner.{{else}}- shared manifests, lockfiles, schemas, workspace configuration, agent instructions have one owner;{{end}}
{{if .Full}}- Dependency and temporal-affinity evidence does not say they should move together.{{else}}- dependency and temporal-affinity evidence does not say they should move together.{{end}}

{{if .Full}}ONE WORKTREE PER WORKER wherever a write path touches workspace configuration: any
project's magusfile, its `magus.yaml`, or a spell source a magusfile imports.

magus READS those files to load the workspace. A half-saved one stops the workspace
loading for everybody in that checkout at once. The rest lose `{{cmd "run"}}`, `{{cmd "ls"}}` and their own tests over an edit they
cannot see. Measured 2026-09-17: three workers
shared a checkout, one saved the root magusfile mid-edit, and all three were
stopped for the duration by a file only one of them had ever opened.
When `{{cmd "job fork"}}` refuses one, the answer is a separate worktree, not a narrower
boundary.{{else}}ONE WORKTREE PER WORKER wherever write path touches workspace configuration: any project's magusfile, its `magus.yaml`, or spell source a magusfile imports.
magus READS those files to load workspace; half-saved one stops workspace loading for everybody in that checkout at once.
When `{{cmd "job fork"}}` refuses one -> separate worktree, not narrower boundary.{{end}}

{{if .Full}}Fork records what it could prove in `write_proof` (`alone`, `disjoint` or
`overlapping`), and `{{cmd "ls jobs"}}` prints it. An overlap is recorded, not refused:
sequencing two jobs onto one path is your call, and the row records
whether anybody checked. Answer the guard's shared-checkout reminder with a proof or a
worktree, not a judgment that it looks fine.

Project boundaries alone are insufficient. A declared dependency means group the work
or serialize producer before consumer. Treat strong hidden affinity as a warning.
When evidence is incomplete, reduce parallelism.{{else}}Fork records what it could prove in `write_proof` (`alone`, `disjoint` or `overlapping`); `{{cmd "ls jobs"}}` prints it.
Overlap recorded, not refused: sequencing two jobs onto one path is your call.
Answer guard's shared-checkout reminder with proof or worktree, not judgment that it looks fine.
Project boundaries alone insufficient. Declared dependency -> group work, or serialize producer before consumer.
Strong hidden affinity: warning.
When evidence incomplete -> reduce parallelism.{{end}}

{{if .Full}}## Fork the job, then spawn the worker{{else}}## Fork job, then spawn worker{{end}}

{{if .Full}}The order is fixed: fork the row, spawn the worker with a description naming it, and
only then does the worker take the lease. A spawn whose description names no live row
binds its child to nothing, so every write it makes is graded as
an editor magus cannot attribute.

1. Record the checkpoint you are handing out: `{{cmd "vcs checkpoint"}} -o name` prints
   the revision, plus a dirty-patch digest when the tree is not clean.{{else}}Fixed order: fork row -> spawn worker with description naming it -> only then worker takes lease.
Spawn whose description names no live row binds child to nothing.
1. Record checkpoint handed out: `{{cmd "vcs checkpoint"}} -o name` prints revision, plus dirty-patch digest when tree not clean.{{end}}
2. Fork one job per unit, from flags:

   ```sh
   magus job fork api-store --model <model> \
     --criteria "accounts move to the new store; nothing names the old one" \
     --write-paths 'api/store.go,api/store_test.go,db/migrations/*.sql' \
     --check "go-test api -- -run TestStore" \
     --checkpoint <checkpoint>
   ```

{{if .Full}}   Or fork from a record with `{{cmd "job fork"}} --stdin`, the form that carries goals;
   [reference.md](reference.md) shows one.

   `{{cmd "job fork"}} --schema` prints every field and the newest `schema_version`.
3. Spawn the worker with the description `<parent>/<role> <job>`: two words, the
   first a parent id and a role joined by `/`, the second the job id.
   - A root job has no parent row, so its first word takes any label
     (`orchestrator/feat api-store`).
   - A child reads `api-store/fix migrate`; the guard resolves the job as `migrate`,
     else `api-store/migrate`.
   - The role is a word for the work (`feat`, `fix`, `review`). A workspace spawn
     rule may restrict it, and its refusal names the list.{{else}}   Or fork from record with `{{cmd "job fork"}} --stdin` (form carrying goals); [reference.md](reference.md) shows one.
   `{{cmd "job fork"}} --schema` prints every field and newest `schema_version`.
3. Spawn worker with description `<parent>/<role> <job>`: two words; first: parent id and role joined by `/`; second: job id.
   - Root job has no parent row -> first word takes any label (`orchestrator/feat api-store`).
   - Child reads `api-store/fix migrate`; guard resolves job as `migrate`, else `api-store/migrate`.
   - Role: word for work (`feat`, `fix`, `review`). Workspace spawn rule may restrict it; refusal names list.{{end}}

{{if .Full}}Write paths name FILES or file globs, never a directory. The check is `<target>
<project> [-- args]`, never the gate. [reference.md](reference.md) covers claiming one
declaration in a shared file and forking from a leased worker.

Read the `regenerated outside the write paths` block fork prints: declared outputs a target
rebuilds from sources the job may edit. Widen the paths to cover the ones the job must
regenerate itself, or leave them out and regenerate them after integration.{{else}}Write paths name FILES or file globs, never directory.
Check: `<target> <project> [-- args]`, never gate.
[reference.md](reference.md): claiming one declaration in shared file, forking from leased worker.
Read `regenerated outside the write paths` block fork prints: declared outputs target rebuilds from sources job may edit. Widen paths to cover ones job must regenerate itself, or leave them out, regenerate after integration.{{end}}

{{if .Full}}Render the prompt FROM the row; never type it. `{{cmd "describe job"}} <job>` prints the
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
graded.{{else}}Render prompt FROM row; never type it. `{{cmd "describe job"}} <job>` prints job's own criteria, boundary, check, plus what workspace knows and nobody wrote down.
Two renders of one row byte-identical; hand-typed prompts are not.
Guard grades every command brief presents in shell fence or code span; one it would deny refuses spawn. Line naming command to forbid it ("never ...") not graded.{{end}}

{{if .Full}}Every worker prompt includes its row, its JOB ID, relevant graph evidence, and
any cap the user set for the tree. Require the worker to:
{{else}}Every worker prompt includes row, JOB ID, relevant graph evidence, any cap user set for tree. Require worker to:{{end}}
{{if .Full}}- export `BAGGAGE=magus.lease=<its id>` before it works: that is the W3C
  Baggage channel, and the member is what tells the agent guard whose declared boundary
  to grade a write against, so a worker that never exports it is graded as an editor
  magus cannot attribute. Export `TRACEPARENT` too when your host has one, and add
  `magus.spawner=<your label>` to the baggage: magus records the trace, the parent span
  and the label as CLAIMS, so `{{cmd "session"}} ls` can show who spawned whom, and no
  verdict is ever keyed on them;{{else}}- export `BAGGAGE=magus.lease=<its id>` before working: guard grades its writes only when set;{{end}}
{{if .Full}}- preserve unrelated changes, stay inside write paths, and avoid generated outputs;{{else}}- preserve unrelated changes, stay inside write paths, avoid generated outputs;{{end}}
{{if .Full}}- run only its assigned magus target, in the foreground with a timeout that covers it, and never end its turn while a run is pending.{{else}}- run only assigned magus target, foreground, timeout covering it; never end turn while run pending.{{end}}

{{if .Full}}End every brief with the step that files the result, as the worker's LAST act before
it reports back. A brief that leaves it out gets a prose report and a row that never
moves: the store keeps the write paths held, `{{cmd "job wait"}}`
has nothing to verify, and nobody notices until a sibling is refused over a path
the finished worker no longer needs. The step, with the shape `{{cmd "job exit"}} --schema`
prints:{{else}}End every brief with step filing result, as worker's LAST act before reporting back.
Brief leaving it out -> prose report, row that never moves.
Step, shape from `{{cmd "job exit"}} --schema`:{{end}}

```sh
magus job exit <job> --stdin <<'EOF'
{"schema_version": 2, "job": "<job>",
 "changed_paths": ["api/store.go", "api/store_test.go"],
 "validation": {"command": "magus run go-test api -- -run TestStore", "output_ref": "<ref>"},
 "descendants": [],
 "unresolved_risks": ["what is left, or what the worker could not verify"]}
EOF
```

{{if .Full}}A typed result makes verification mechanical; the same four facts in prose can only be
graded by reading, and a worker that ran a filtered subset writes the same
paragraph as one that did not. Keep unresolved risks mandatory, so a mis-scoped
worker can say so instead of widening silently.

A worker writes only its own row and the children it forks. Every other store write
is the orchestrator's; a refused worker reports it as an unresolved risk and stops.{{else}}Typed result makes verification mechanical. Unresolved risks mandatory -> mis-scoped worker can say so, not widen silently.
Worker writes only own row and children it forks. Every other store write: orchestrator's; refused worker reports it as unresolved risk, stops.{{end}}

{{if .Full}}After the spawn, the row carries the job:
{{else}}After spawn, row carries job:{{end}}
{{if .Full}}- A worker records the base it LANDED on, which can differ from the checkpoint you
  handed it.{{else}}- Worker records base it LANDED on; can differ from handed checkpoint.{{end}}
{{if .Full}}- A denied worker coordinates and never works around.{{else}}- Denied worker coordinates, never works around.{{end}}
{{if .Full}}- Every row ends in pass, fail, or NO-RETURN, and the root writes which: silence is
  not a pass.{{else}}- Every row ends in pass, fail, or NO-RETURN; root writes which; silence is not pass.{{end}}
{{if .Full}}- Only a row with a check or a goal can pass. Fork a scout `--read-only`; it passes only
  through a script check (reference.md), and a prose scout ends no_return by design.

[reference.md](reference.md) covers bases, read paths, releasing a path, moving a live
job's boundary, how magus ends abandoned jobs, and what `{{cmd "job wait"}}` checks.{{else}}- Only row with check or goal can pass. Fork scout `--read-only`; passes only through script check (reference.md); prose scout ends no_return by design.
[reference.md](reference.md): bases, read paths, releasing path, moving live job's boundary, how magus ends abandoned jobs, what `{{cmd "job wait"}}` checks.{{end}}

{{if .Full}}## Declare the criteria magus can check for you{{else}}## Criteria magus checks for you{{end}}

{{if .Full}}A GOAL is the part of a job's criteria magus grades itself, from evidence the worker
cannot author. It is a check that passed, or paths or symbols changed, present, absent
or unreferenced. Write goals in the record's `goals`. `{{cmd "job wait"}}` refuses pass until
each verifies. [reference.md](reference.md) has the table and the rename pattern.{{else}}GOAL: part of job criteria magus grades itself, from evidence worker cannot author: check that passed, or paths or symbols changed, present, absent, unreferenced.
Write goals in record's `goals`; `{{cmd "job wait"}}` refuses pass until each verifies.
[reference.md](reference.md): table, rename pattern.{{end}}

{{if .Full}}## Observe through the correct control plane{{else}}## Observe through correct control plane{{end}}

{{if .Full}}Run workers non-blocking; block on one only when your next action needs its result.
Watch processes with `{{cmd "status"}} --watch=15s`. Never message a worker to ask how it
is going: `{{cmd "describe job"}} <job>` grades where it stands, and `{{cmd "job watch"}} <job>`
shows it happening. Message a worker only to CHANGE what it was handed.
[reference.md](reference.md) covers blocked workers and auditing what a job ran.{{else}}Run workers non-blocking; block on one only when next action needs its result.
Watch processes: `{{cmd "status"}} --watch=15s`.
Never message worker asking progress: `{{cmd "describe job"}} <job>` grades where it stands; `{{cmd "job watch"}} <job>` shows it happening.
Message worker only to CHANGE what it was handed.
[reference.md](reference.md): blocked workers, auditing what job ran.{{end}}

## Integrate and verify

As jobs finish:
{{if .Full}}
1. Compare the store against the ACTUAL diff since each job's checkpoint, not the
   paths it reported (`{{cmd "graph diff"}} --rev <revision>` for the domain). A differing
   dirty digest means it saw a tree you are not diffing.{{else}}1. Compare store against ACTUAL diff since each job's checkpoint, not paths reported (`{{cmd "graph diff"}} --rev <revision>` for domain). Differing dirty digest: it saw tree you are not diffing.{{end}}
{{if .Full}}2. Run `{{cmd "job wait"}} <job>` on each returned job BEFORE you read its result. It
   checks what is mechanical:
   - every changed path inside the declared write paths and outside the denied ones;
   - a change set that is not empty on a row that writes;
   - the descendants the store carries;
   - the output ref, bound to a run of THAT JOB'S OWN CHECK which the store recorded
     as PASSING. There is no `passed` field on the result: wait reads the
     ref's own recorded attempt from the output store and derives the outcome from
     it, never from what the worker claims.{{else}}2. `{{cmd "job wait"}} <job>` on each returned job BEFORE reading its result. Checks what is mechanical:
   - every changed path inside declared write paths, outside denied ones;
   - change set not empty on row that writes;
   - descendants store carries;
   - output ref, bound to run of THAT JOB'S OWN CHECK store recorded as PASSING.{{end}}

{{if .Full}}   A job that verifies is recorded `pass`. A rejection exits 1 naming every violation;
   a result that will not decode exits 2. A holder does not verify its own job, or a
   sibling's.

   Then reopen the evidence yourself (`{{cmd "query output"}} <ref>`). Wait proves the ref
   names a passing run of this job's own check, never that the work meets the job's
   GOAL. A worker's prose about its criteria is not that evidence.
3. Resolve cross-job API changes centrally; never assign the same seam twice.{{else}}   Verifying job recorded `pass`. Rejection exits 1 naming every violation; undecodable result exits 2. Holder does not verify own job, or sibling's.
   Then reopen evidence yourself (`{{cmd "query output"}} <ref>`). Wait proves ref names passing run of job's own check, never that work meets job's GOAL. Worker prose about criteria is not that evidence.
3. Resolve cross-job API changes centrally; never assign same seam twice.{{end}}
4. Regenerate declared outputs once, after source work converges.
{{if .Full}}5. Re-run `{{cmd "affected"}} <target> --plan` over the actual diff. If its shape
   invalidates the original partition, stop parallel integration and reconcile.{{else}}5. Re-run `{{cmd "affected"}} <target> --plan` over actual diff. If its shape invalidates original partition -> stop parallel integration, reconcile.{{end}}
{{if .Full}}6. Read the integrated changeset with `{{cmd "diff"}} --impact` before landing it. It shows
   what the fleet's combined edit reaches, who else has been changing it, and a
   rebuild estimate from recorded run times. It adds what the advisors say and any
   note anchored to a file it touched. Context, never a verdict: nothing gates on it and
   the exit code is unchanged, and a section that is empty means nobody could
   measure it, not that nothing was found.{{else}}6. Read integrated changeset with `{{cmd "diff"}} --impact` before landing: reach, other recent editors, rebuild estimate, advisors, anchored notes. Context, never verdict. Empty section: nobody could measure it, not nothing found.{{end}}
{{if .Full}}7. Run `{{cmd "affected"}} ci` and evaluate the top-level acceptance criteria.{{else}}7. Run `{{cmd "affected"}} ci`; evaluate top-level acceptance criteria.{{end}}

{{if .Full}}Parallelism is an optimization, not the objective. Fewer
well-isolated jobs are usually cheaper than wide fan-out followed by conflict
repair, and the graph is evidence for that judgment rather than permission to
spawn every possible worker.{{else}}Prefer fewer proven-independent jobs over wide fan-out and conflict repair.{{end}}
