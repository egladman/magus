# Splitting work across agents

Count the WRITE SETS your change needs (the distinct groups of files to edit), not the
projects it invalidates. Getting this backwards is the standard fan-out failure. A
one-line edit in a central package invalidates half the workspace and is still one
edit{{if .Full}}; fanned out, it puts several agents on one file{{end}}.

`{{cmd "affected"}} <target> --plan` partitions VALIDATION: which targets to run, grouped
for runner balance. It is neither an edit assignment nor a proof of write isolation.
So it can veto a fan-out and never license one. One shard means keep the work local.{{if .Full}}
Several mean the testing parallelizes, and the editing is still your question.{{end}}

Before any edits exist, the shard plan is empty and proves nothing. Finding the
candidate paths IS the partitioning work, done with the graph{{if .Full}}, not by intuition{{end}}:

```sh
magus graph build                           # first in a fresh worktree, or refs answers "unknown, not absent"
magus refs <symbol> --occurrences -o json   # every edit site, column-precise
magus explain <node>                        # one node's edges and blast radius
magus affected ci --plan --stdin            # plan PROPOSED paths, before editing
```

You do not need to be asked to split work{{if .Full}}. "Split this across agents" is one way in;
several disjoint write sets you can name is another{{else}}: several disjoint write sets
you can name are reason enough{{end}}.

{{if .Full}}What costs is unbounded fan-out, not fan-out: a hand-on without a shrinking scope,
a job with no criteria, a principal model on mechanical edits. {{end}}Say what a round
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
`{{cmd "job exec"}} <row>` until `{{cmd "job exit"}} <row>` ([the root's role](reference.md#the-roots-role)).

## Coalesce what the write sets allow

Disjoint write sets LICENSE parallelism; they do not require it. Every worker carries
a fixed load before it reads a line of the diff{{if .Full}} (system prompt, instruction
file, routing index, skills, its own graph queries), so ask whether each unit is big
enough to be worth one{{end}}.

- A depends-on chain is ONE worker in sequence, not two workers in turn{{if .Full}}.
  Two is two loads for one unit of work, and the second starts by rediscovering
  what the first just learned{{end}}.
- For the rest, read the grouping metric (`{{buzz "workspace.grouping"}}` through `{{tool "client"}}`,
  or `grouping history` in `{{cmd "describe job"}} <job>`) and weigh each measured shape
  against the others{{if .Full}}: chained, split and solo each report the load before a
  first write and the context, wall time and collisions per group{{end}}. It reports
  and never decides{{if .Full}}; a person writes any standing grouping policy{{end}}.
- Where the lens answers unknown, apply these defaults and say in the plan that you
  did. Merge small disjoint units inside one project. Spawn only when more than one
  independent unit survives the merge. Fill idle root time from that pool, never by
  splitting finer.

## Run the graph-engineering loop

Prove it before you plan it. A claim a plan or brief rests on cites the output ref of
a run that settled it. A guess nobody ran stays out of the plan.

{{if .Full}}The loop: state the goal and acceptance criteria, partition, hand out, observe,
evaluate against the evidence, revise ownership or ordering, then integrate. Graph engineering
is a natural evolution of loop engineering: it improves the partition, never replaces the loop.

Acceptance evidence is an output ref the root reopens (`{{cmd "query output"}} <ref>`),
never a worker's prose: a worker that ran a filtered subset, or restated its criteria
into ones it passed, reports success either way. No job is complete until its criteria
and assigned check pass, and the root, not a worker, decides whether the top-level goal
is complete.{{else}}The loop: define, partition, hand out, observe, evaluate, course-correct, integrate.

- Acceptance evidence is an output ref the root reopens (`{{cmd "query output"}} <ref>`),
  never a worker's prose.
- A worker is not complete until its criteria and assigned check pass.
- The root agent decides whether the top-level goal is complete.{{end}}

## Keep worker records out of the user update

{{if .Full}}Read a worker's structured result, not its whole event stream. Keep its job id,
outcome, changed paths, validation refs and unresolved risks; reopen the relevant
evidence when verification needs it. Persist the complete JSON or JSONL in the
host capture or result file. Do not echo it into another tool call or the reply.

For a job validation, project `problems`, `unchecked` and a count of `permitted`
from `-o json` before it reaches your context. Keep the producer's failure status
when piping. Inspect individual permitted deviations only when they affect a
decision; `--dry-run` text intentionally previews the full record. Prepare the
spec once in a scratch file (`job_spec` below); do not retype its full exception
list on every retry:{{else}}Read worker's structured result, never whole event stream. Keep job id, outcome, changed paths, validation refs, unresolved risks.
Persist full JSON/JSONL in host capture or result file; never echo it into another call or reply.
Job validation: project `problems`, `unchecked`, count of `permitted` from `-o json`; keep producer's failure status when piping.
Inspect a permitted deviation only when it affects a decision. Spec once in scratch file (`job_spec`); never retype exception list per retry:{{end}}

```sh
set -o pipefail
{{cmd "job fork"}} --stdin --dry-run -o json < "$job_spec" |
  jq -e 'if .job.id then {job: .job.id, problems, unchecked, permitted_count: (.permitted // [] | length)} else error("missing job validation") end'
```

{{if .Full}}Tell the person what changed, what passed, what remains unverified, and any decision
they need to make. A routine claim, handled denial, or recorded deviation is not an
update; collapse repeats into the current blocker and next action. A waiver that skips
validation or accepts a material risk gets one plain sentence and its evidence link,
never pasted waiver objects, claim lists or guard transcripts.{{else}}Tell person: what changed, passed, remains unverified, decisions needed. Routine claim, handled denial, recorded deviation: not an update; collapse repeats into current blocker + next action.
Waiver skipping validation or accepting material risk: one plain sentence + evidence link, never pasted waiver objects, claim lists, guard transcripts.{{end}}

## Declare the interface before any job forks

When a change adds a shared API, the ROOT names it before any edit. A shared
API is a module several call sites use, a type, an event, or an exported
function. Name its path, every exported name with its signature, and the EXISTING
symbols it must reuse, not restate.

Workers implement the names they were handed and never coin a public one. A name
invented at each call site is how one concept ends up with five spellings
{{- if .Full}}, and how a plan that says "collapse two state types into one" ships
a third: each site made a locally sensible choice and nobody owned the set{{end}}.

Then make the declaration gradeable{{if .Full}}, so the names are a contract, not a suggestion{{end}}:

- `symbol` + `present` for each new exported name.
- `symbol` + `absent` for the name a second copy would predictably take beside a
  symbol the work must reuse. No gate grades reuse itself{{if .Full}}: `present`
  holds for a symbol that existed before the job began, and a reference count
  cannot tell the defining file or an import from real use{{end}}.
- `symbol` + `unreferenced` for each helper the shared API replaces.

A job that adds or changes an exported API gets a `symbol` + `signature` goal by
default. A stub job lands the stub, and the root runs `{{cmd "graph build"}}`. The root
then forks the `signature` goal empty. The fork records the indexed stub as the
contract, beside a `changed` goal on the same name.

A job's `breaks` list names the flags, JSON keys, exported symbols or stored formats
you approved it breaking. A worker that finds a break outside it stops and reports.
Each unreferenced symbol `{{cmd "job fork"}}` lists in the write paths
(`deletion_candidates`) or `{{cmd "job wait"}}` reports is finished or deleted. Confirm
first{{if .Full}}: the graph cannot see reflection, interface dispatch or outside consumers{{end}}.

Check each declared name with `{{cmd "refs"}}` before forking.{{if .Full}} A bare name that resolves
to more than one definition is graded against one of them, silently.{{end}}

This applies with zero workers. A solo change across several call sites is a one-job
tree. Skipping the declaration because nothing is forked is the same failure{{if .Full}}: write
the table, check it with `{{cmd "refs"}}` when the edits land, then report the change done{{end}}.

## Set one topology boundary

Before spawning, state the topology: the model per job, and whether isolated worktrees are available.

{{if .Full}}- Fan-out and depth are not capped unless the workspace sets a cap. Spawn as many
  jobs, nested as deep, as the partition supports.
- A limit exists only when magus.yaml's `jobs` section sets one. Read it with
  `{{cmd "config view"}}` or `{{tool "config"}}`.
  - `max_depth` and `max_live` make `{{cmd "job fork"}}` refuse past them, naming the key.
  - `default_timeout` bounds a fork that names no `--timeout`.
  - Honor a cap the user states the same way.
- Editing costs the workspace nothing; VALIDATION contends: the magus runs a job
  triggers, which share the machine's build budget. A run kept out of that budget is
  refused at once (MGS3009) naming who holds it, and waits in its own call only when
  `budget_wait` (default 0; `--budget-wait 30m`) says so; a spawn or `{{cmd "job fork"}}`
  into a full one says how full it is.
  Serialize validations that share a worktree, even when their write sets are disjoint.

Assign the check from the pipeline the workspace composed, not from convention.
`{{cmd "describe target"}} ci <project>` names what `ci` chains and in what order.

- A job gets the narrowest check that covers its edits: the target from that
  decomposition. Scope it to the job's own tests with the runner's filter after `--`
  where one exists. A go test check naming exactly the packages of the write paths
  (`go::go-test . -- ./pkg/... -run 'Store'`) may filter their tests too. Never the full suite.
- The integration job owns the full `go-test` and `lint`, and runs every check a merged
  worker filed as owed. The integrator re-runs the described order, and `{{cmd "affected"}} ci` re-proves the whole composition.{{else}}- No cap on fan-out or depth unless magus.yaml `jobs` sets one (`{{cmd "config view"}}`, `{{tool "config"}}`): `max_depth`, `max_live` refuse past them; `default_timeout` bounds a fork with no `--timeout`. User-stated cap: same.
- Editing costs nothing; VALIDATION contends for machine build budget. A run kept out is refused at once (MGS3009) and waits in its own call only when `budget_wait` (default 0) says so; spawn or `{{cmd "job fork"}}` into full budget says how full.
- Serialize validations sharing a worktree, even with disjoint write sets.

Assign check from composed pipeline, never convention: `{{cmd "describe target"}} ci <project>` names what `ci` chains, in order.
- Job: narrowest target covering its edits, scoped with runner filter after `--` (`go::go-test . -- ./pkg/... -run 'Store'`). Never full suite.
- Integration job: full `go-test`, `lint`, every check a merged worker filed owed; re-runs described order; `{{cmd "affected"}} ci` re-proves composition.{{end}}

A job's check is that narrow target, never the gate. The root gates ONCE, in its own
tree, after every unit lands{{if .Full}}, which is what stops seven fanned-out workers from
each running the whole pipeline concurrently on one machine{{end}}.

Decide each job's validation PLANE with its target. Some worker environments cannot
execute magus at all{{if .Full}}. Examples are an isolated tree with no usable binary, or a guard
routing raw tools to targets it cannot run{{end}}. Such a worker cannot validate what it
writes. Mark that job's check ROOT-DEFERRED when you fork it:

- the worker writes the tests, stops at the static checks its environment does run,
  and says so;
- the root executes the job's target centrally before verifying.{{if .Full}} Otherwise each
  worker reports "done" with nothing executed, which `{{cmd "job wait"}}` refuses to pass.{{end}}

A worker may fork part of its own job. It may not hand it out without shrinking the
problem{{if .Full}}; that is the shape that does not terminate, and
the cost people attribute to "multi-agent" is almost always this{{end}}. These instructions give it a
definitive end without capping depth:

- **Every level narrows.** A child's scope is a strict subset of its parent's. A
  worker that would hand on its whole job does the work instead.
- **Every job carries acceptance criteria down.** A child inherits its parent's
  criteria plus its own.{{if .Full}} A job nobody can evaluate cannot end; that, not the nesting,
  makes depth dangerous.{{end}}
- **A retry is `{{cmd "job fork"}} <same id>`, never a new id.** The next holder reads the previous
  attempt in `{{cmd "describe job"}}`, never a transcript. One marked not retryable names
  what changes first (`{{cmd "job apply"}}` to widen, a config edit, or you). Make it, or
  do the job locally; never re-fork it unchanged.

  A goal unmet twice marks it so: the root re-scopes the job, or serializes it behind
  whatever keeps breaking it{{if .Full}}. Two jobs with an undeclared dependency each break
  the other's criteria, and re-forking the failing one satisfies every instruction above
  while alternating forever. The budget is what ends it{{end}}.
- **Whatever the parent does not hand out, the parent still owns.** A strict subset
  leaks by construction.{{if .Full}} Split "no caller of X remains" into per-project jobs, and
  the callers in no project belong to nobody. Every job passes and the goal is
  unmet.{{end}} Carry a remainder row at each level and close it explicitly.

START EVERY SPAWN AT THE ECONOMY TIER, and SAY which model you picked. Move a job up
only for work the table below assigns higher. The fit still runs both ways: an
ambiguous API boundary does not get the cheapest model because it looked like less
work{{if .Full}}. Judge cost per COMPLETED job: a cheap worker that needs a rerun is not
cheaper{{end}}.

Naming it is the checkable half: every spawn names a model, or an agent definition
that names one.

- Inheriting the parent's model ON PURPOSE is fine and often right{{if .Full}}, such as a hard review under a cheaper coordinator{{end}}.
- Inheriting it BY OMISSION is the failure. A host defaulting to "same as the parent"
  makes every unnamed spawn the most expensive one, and records no choice.

When the tier is unclear, start at economy. A job that fails its criteria there is
re-forked one tier up, not retried at the same one.{{if .Full}} Economy is a starting
point, not a ceiling: same-strength offload is legitimate.{{end}}

Tiers are the workspace's{{if .Full}}: a magusfile enum (`magus\modelTiers`) maps each to every
harness's model alias, and the guard compares a spawn's tier by rank{{end}}. `--model` takes a tier
or a model. magus never picks a model live: re-fork to change a tier.

| Shipped tier | Assign |
|---|---|
| principal | architecture, ambiguous ownership, public APIs, migrations, security, integration |
| standard | isolated implementation with a clear contract and a bounded set of projects, and tracing behavior through code |
| economy | mechanical edits, fixtures, docs, inventory, and lookups the graph answers (exists, depends-on, is-generated) |

If the host cannot select models or reasoning effort, keep its default. Tool access
is a separate axis: evidence gathering, scouting, and review get read-only tools
where the host offers them. Never downgrade the root integration pass or final
release gate.

To prove something, brute-force it at economy{{if .Full}}. Many economy workers finish about as
fast as one principal worker, for far fewer tokens{{end}}:

- Fork one read-only job per claim, input, or variant, and run them all at once.
  Give each a script check, so `{{cmd "job wait"}}` can pass it.
- Spawn the `magus-scout` agent for each where your harness installed it.
- Each reports its command and output ref{{if .Full}}, so evidence settles the claim and not a worker's reading{{end}}.
- The principal tier only reconciles the claims where workers disagree.

Keep economy jobs short-lived. The brief points at evidence (paths, output refs, the
row) and carries no transcript.{{if .Full}} Some providers raise a tier's price past a prompt size.{{end}}
Where the brief names that size, the worker reports and stops before passing it.

Nesting is allowed when the host supports it, but it creates no new budget and no
private ownership map. A child reports its proposed descendants to its parent before it
spawns them.{{if .Full}} The store carries each with its parent, model, criteria and write paths.{{end}}

Descendants inherit the ancestor's deny paths and may subdivide only its write paths. A
cap the user sets applies to the whole tree, not once per parent.

Keep one integration owner per landing checkout, the integrator, never the root. A
child coordinates its descendants but may not accept changes outside its own job or
relax the root's criteria.

## Seed the partition with magus

Choose the target that will validate the work. `ci` is the release gate, but any
target `{{cmd "affected"}} <target>` accepts can be planned:

```sh
magus affected <target> --plan
```

For a proposed change whose paths are known but not yet edited, plan those paths
instead of the current diff (`--stdin --plan`, one repo-relative path per line).
Read the JSON fields `count`, `max_parallel`, `source`, and `matrix`.{{if .Full}} A shard is a
history-balanced execution group, not an edit assignment or proof of write isolation.{{end}}
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
- Affinity stays with `{{tool "client"}}` (`{{buzz "history.affinity"}}`). Without MCP,
  run `{{cmd "buzz"}} -e` printing `magus\history().affinity()`.
- Use `{{cmd "refs"}} <symbol>` when two jobs may touch the same API.

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
loading for everybody in that checkout{{if .Full}}, who lose `{{cmd "run"}}`, `{{cmd "ls"}}`
and their own tests over an edit they cannot see{{end}}.
When `{{cmd "job fork"}}` refuses one, the answer is a separate worktree, not a narrower
boundary: fork with `--checkout <dir>` outside the workspace, never by hand.

Fork records what it could prove in `write_proof` (`alone`, `disjoint` or
`overlapping`), and `{{cmd "ls jobs"}}` prints it. An overlap is recorded, not refused:
sequencing two jobs onto one path is your call{{if .Full}}, and the row records
whether anybody checked{{end}}. Answer the guard's shared-checkout reminder with a proof or a
worktree, not a judgment that it looks fine.

Project boundaries alone are insufficient. A declared dependency means group the work
or serialize producer before consumer. Treat strong hidden affinity as a warning.
When evidence is incomplete, reduce parallelism.

## Fork the job, then spawn the worker

The order is fixed: fork the row, spawn the worker with a description naming it, and
only then does the worker take the lease. A spawn whose description names no live row
binds its child to nothing{{if .Full}}, so every write it makes is graded as
an editor magus cannot attribute{{end}}.

1. Record the checkpoint you are handing out: `{{cmd "vcs checkpoint"}} -o name` prints
   the revision, plus a dirty-patch digest when the tree is not clean.
2. Fork one job per unit, from flags:

   ```sh
   magus job fork api-store --model <tier or model> \
     --criteria "accounts move to the new store; nothing names the old one" \
     --write-paths 'api/store.go,api/store_test.go,db/migrations/*.sql' \
     --check "go-test api -- -run TestStore" \
     --checkpoint <checkpoint>
   ```

   Or fork from a record with `{{cmd "job fork"}} --stdin`, the form that carries goals;
   [reference.md](reference.md) shows one.

   Before every fork, run `{{cmd "job fork"}} --stdin --dry-run`: it lists every problem with its code and fix, and writes nothing.
   `{{cmd "job fork"}} --schema` prints every field and the newest `schema_version`.
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

Render the prompt FROM the row; never type it. `{{cmd "describe job"}} <job>` prints the
job's own criteria, boundary and check, plus what the workspace knows and nobody wrote
down{{if .Full}}: the projects the write paths reach, the declared
output globs that land inside them, the paths a sibling job is holding, the build
inputs and workspace configuration that have one owner, and the projects that
change alongside the leased ones without declaring a dependency{{end}}.{{if .Full}} Two renders of one
row are byte-identical; hand-typed prompts are not.{{end}}

The guard grades every command a brief presents in a shell fence or a code span. One
it would deny refuses the spawn{{if .Full}}, since the worker runs a brief's
commands as written{{end}}. A line that names a command to forbid it ("never ...") is not graded.

### What a brief leaves out

{{if .Full}}A brief carries the JOB ID, the goal and why, the decisions made, and evidence refs.
It leaves out skill loads, command style, the `BAGGAGE` export, write paths, the check,
how to commit and push, and attribution: the guard, the harness and `{{cmd "describe job"}}`
supply or enforce each. A restated guard rule costs every worker tokens and goes stale;
when a worker keeps missing something, fix the guard rule or its message, not the brief.
A follow-up message to a bound worker carries only what changed.{{else}}Brief carries only: JOB ID, goal and why, decisions made, evidence refs.
Leave out: skill loads, command style, `BAGGAGE` export, write paths, check, how to commit and push, attribution; guard, harness, `{{cmd "describe job"}}` supply or enforce each.
Restated guard rule costs every worker tokens, goes stale. Worker keeps missing something -> fix guard rule or its message, never brief.
Follow-up message to bound worker: only what changed.{{end}}

Every worker prompt includes its row, its JOB ID, relevant graph evidence, and
any cap the user set for the tree. Require the worker to:

- export `BAGGAGE=magus.lease=<its id>` before it works: the guard grades its writes only
  when that is set{{if .Full}}. Add `magus.spawner=<your label>` to the baggage, and
  `TRACEPARENT` where the host has one: magus records them as claims for
  `{{cmd "session"}} ls`, and no verdict is keyed on them{{end}};
- preserve unrelated changes, stay inside write paths, and avoid generated outputs;
- run only its assigned magus target, in the foreground with a timeout that covers it, and never end its turn while a run is pending;
- never wait for the build budget outside its check's own call. When its check is refused (MGS3009), having waited as long as `budget_wait` allows (none by default), it commits and files the check as owed, `"owed": "no seat"` in `validation` or that goal's `verification_evidence`, with no output ref. `{{cmd "job wait"}}` passes that result and the integration runs the check;
- never exit its row while it waits on a reply from you: ending its turn with the question as its final text leaves the row open and the worker resumable.

End every brief with the step that files the result, as the worker's LAST act before
it reports back. A brief that leaves it out gets a prose report and a row that never
moves{{if .Full}}: the store keeps the write paths held, `{{cmd "job wait"}}`
has nothing to verify, and nobody notices until a sibling is refused over a path
the finished worker no longer needs{{end}}. The step: `{{cmd "job exit"}} <job> --stdin`, filing the shape `{{cmd "job exec"}} <row>` printed.
Never inline the result fields in a brief; the row prints them filled in{{if .Full}}, and
[reference.md](reference.md) shows a record{{end}}.
{{if .Full}}A typed result makes verification mechanical. {{end}}Keep unresolved risks mandatory{{if .Full}}, so a
mis-scoped worker can say so instead of widening silently{{end}}.

A worker writes only its own row and the children it forks. Every other store write
is the orchestrator's; a refused worker reports it as an unresolved risk and stops.

After the spawn, the row carries the job:

- A worker records the base it LANDED on, which can differ from the checkpoint you
  handed it.
- A denied worker coordinates and never works around.
- Every row ends in pass, fail, or NO-RETURN, and the root writes which: silence is
  not a pass.
- Only a row with a check or a goal can pass. Fork a scout `--read-only`; it passes only
  through a script check (reference.md) or by filing a result: a read-only row whose holder files a result is
  recorded pass, and only a row with nothing filed ends no_return.

[reference.md](reference.md) covers bases, read paths, releasing a path, moving a live
job's boundary, how magus ends abandoned jobs, and what `{{cmd "job wait"}}` checks.

## Declare the criteria magus can check for you

A GOAL is the part of a job's criteria magus grades itself, from evidence the worker
cannot author. It is a check that passed, or paths or symbols changed, present, absent
or unreferenced. Write goals in the record's `goals`. `{{cmd "job wait"}}` refuses pass until
each verifies. [reference.md](reference.md) has the table and the rename pattern.

## Observe through the correct control plane

Run workers non-blocking; block on one only when your next action needs its result.
`{{cmd "status"}} --watch=15s` is for a person, never an agent's background task: an agent waits
for its own run's completion notice.

The root reads `{{cmd "describe job"}} <job>` or `{{cmd "job watch"}} <job>` only when it needs to know.
Never message a worker to ask how it is going, only to CHANGE what it was handed.
[reference.md](reference.md) covers blocked workers and auditing what a job ran.

## Integrate and verify

The root never builds. The integrator is a role, never a check: spawned `<parent>/integrate
<job>` unisolated in the landing checkout, it cherry-picks, builds, regenerates and commits.
Its merge (`hack/dev/merge-job-branches.buzz --apply`) then recomposes the landing
branch's unpushed commits into one per merged job, each output beside its source, with no
merge commit left and the merged tree byte-identical. A commit a remote ref already holds stays.

As jobs finish: compare the store to the ACTUAL diff since each checkpoint; run
`{{cmd "job wait"}} <job>` before reading each result; reopen evidence with
`{{cmd "query output"}} <ref>`; resolve shared seams once; regenerate declared outputs
once; re-plan with `{{cmd "affected"}} <target> --plan` if the partition broke; read
`{{cmd "diff"}} --impact` for context only; validate once per wave; finish with
`{{cmd "affected"}} ci`. [reference.md](reference.md#integrate-and-verify) has the
mechanical wait checks and the wave loop.
