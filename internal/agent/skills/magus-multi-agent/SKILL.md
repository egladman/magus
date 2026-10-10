# Splitting work across agents

Count the WRITE SETS your change needs (the distinct groups of files to edit), not the
projects it invalidates. Getting this backwards is the standard fan-out failure{{if .Full}}: a
one-line edit in a central package invalidates half the workspace and is still one
edit, and fanning it out produces several agents editing one file{{else}}. A
one-line edit in a central package invalidates half the workspace and is still one
edit{{end}}.

`{{cmd "affected"}} <target> --plan` partitions VALIDATION: which targets to run, grouped
for runner balance. It is neither an edit assignment nor a proof of write isolation.
So it can veto a fan-out and never license one. One shard means keep the work local.
Several mean the testing parallelizes, and the editing is still your question.

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

Hand every spawn a job row forked first, read-only scouts included. A child bound to
no row is graded as you are, and no lease bounds you. "Fork the job, then spawn the
worker" below has the commands.

Fan-out is not inherently expensive{{if .Full}}. What costs is unbounded fan-out:
workers that hand work on without a shrinking scope, jobs with no acceptance criteria
so nobody can say when to stop, and a principal model assigned to mechanical edits.
Each of those is a choice made below, not a property of fanning out{{end}}. Say what a
round costs when the user is deciding, and prefer the smallest fan-out that covers
the work.

Fan out only after the collision check below REPORTS the jobs disjoint; looking
separate is not that check. With only one coherent write set, keep the work local.
The root agent owns the goal, the budget, the topology, integration, and final
verification, and never hands those out.

## Coalesce what the write sets allow

Disjoint write sets LICENSE parallelism; they do not require it. Every worker carries
a fixed load before it reads a line of the diff{{if .Full}}: a system
prompt, the repository's instruction file, the routing index, the skills that
load, and the graph queries it runs to find its own footing, spent identically
whether the unit is fifty lines or five hundred{{end}}. So after partitioning, ask whether
each unit is big enough to be worth a worker.

Apply these four in the order they bite:

- A depends-on chain is ONE worker in sequence, not two workers in turn{{if .Full}}.
  Two is two loads for one unit of work, and the second starts by rediscovering
  what the first just learned{{end}}.
- Merge small disjoint units inside one project. Splitting them usually gains less
  wall clock than the load you pay twice{{if .Full}}, and
  they contend on the same validation anyway{{end}}.
- Spawn only when more than one independent BIG unit survives the merge. One unit is
  inline work{{if .Full}}: a brief longer than the diff it asks for is the
  tell{{end}}.
- Fill idle root time from that pool, never by splitting finer. A root blocked on a
  gate is a reason to start the next merged unit{{if .Full}}, and cutting a unit
  in half to have something to spawn buys wall clock with two fixed loads{{end}}.

## Run the graph-engineering loop

Prove it before you plan it. A claim a plan or brief rests on cites the output ref of
a run that settled it. A guess nobody ran stays out of the plan.

{{if .Full}}Graph engineering is a natural evolution of loop engineering. The
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

Acceptance evidence is an output ref the root reopens (`{{cmd "query output"}} <ref>`),
never a worker's prose. A worker that ran a filtered subset, or quietly restated its
criteria into something it did pass, reports success either way, and a
transcript cannot tell you which happened.

An agent may report that its edits are done, but no job is complete until its
acceptance criteria and assigned check pass. The root agent, not a worker,
decides whether the top-level goal is complete.{{else}}The loop: define, partition, hand out, observe, evaluate, course-correct, integrate.

- Acceptance evidence is an output ref the root reopens (`{{cmd "query output"}} <ref>`),
  never a worker's prose.
- A worker is not complete until its criteria and assigned check pass.
- The root agent decides whether the top-level goal is complete.{{end}}

## Declare the interface before any job forks

When a change adds a shared API, the ROOT names it before any edit. A shared
API is a module several call sites use, a type, an event, or an exported
function. Name:

- the path;
- every exported name with its signature;
- the EXISTING symbols it must reuse, not restate.

Workers implement the names they were handed and never coin a public one. A name
invented at each call site is how one concept ends up with five spellings
{{- if .Full}}, and how a plan that says "collapse two state types into one" ships
a third: each site made a locally sensible choice and nobody owned the set{{end}}.

Then make the declaration gradeable, so the names are a contract, not a suggestion:

- `symbol` + `present` for each new exported name.
- `symbol` + `absent` for the name a second copy would predictably take beside a
  symbol the work must reuse. No gate grades reuse itself{{if .Full}}: `present`
  holds for a symbol that existed before the job began, and a reference count
  cannot tell the defining file or an import from real use{{end}}.
- `symbol` + `unreferenced` for each helper the shared API replaces.

Check each declared name with `{{cmd "refs"}}` before forking. A bare name that resolves
to more than one definition is graded against one of them, silently.

This applies with zero workers. A solo change across several call sites is a one-job
tree. Skipping the declaration because nothing is forked is the same failure{{if .Full}} without
a fork to blame. Write the table in the
conversation, check it with `{{cmd "refs"}}` when the edits land, and only then report
the change done{{end}}.

## Set one topology boundary

Before spawning, state the topology: the model per job, and whether isolated
worktrees are available.

- Fan-out and depth are not capped unless the workspace sets a cap. Spawn as many
  jobs, nested as deep, as the partition supports.
- A limit exists only when magus.yaml's `jobs` section sets one. Read it with
  `{{cmd "config view"}}` or `{{tool "config"}}`.
  - `max_depth` and `max_live` make `{{cmd "job fork"}}` refuse past them, naming the key.
  - `default_timeout` bounds a fork that names no `--timeout`.
  - Honor a cap the user states the same way.
- Editing costs the workspace nothing; VALIDATION contends: the magus runs a job
  triggers. Read the live pool (`{{cmd "status"}}`) before starting a validation, not
  before starting an agent. Serialize validations that share a worktree, even when
  their write sets are disjoint.

Assign the check from the pipeline the workspace composed, not from convention.
`{{cmd "describe target"}} ci <project>` names what `ci` chains and in what order.

- A job gets the narrowest target from that decomposition.
- The integrator re-runs the described order, and `{{cmd "affected"}} ci` re-proves the
  whole composition.{{if .Full}} A worker hand-sequencing lint, format, and test is re-deriving an
  order the magusfile already owns, and the step it forgets fails silently by
  omission.{{end}}

A job's check is that narrow target, never the gate. The root gates ONCE, in its own
tree, after every unit lands{{if .Full}}, which is
what stops seven fanned-out workers from each running the whole pipeline
concurrently on one machine{{end}}.

Decide each job's validation PLANE with its target. Some worker environments cannot
execute magus at all. Examples are an isolated tree with no usable binary, or a guard
routing raw tools to targets it cannot run. Such a worker cannot validate what it
writes. Mark that job's check ROOT-DEFERRED when you fork it:

- the worker writes the tests, stops at the static checks its environment does run,
  and says so;
- the root executes the job's target centrally before verifying.{{if .Full}} Leaving each worker to discover the wall
  spends its budget on the discovery, once per worker, and its report then reads
  "done" with nothing executed, which `{{cmd "job wait"}}` already refuses to record
  as a pass.{{end}}

A worker may fork part of its own job. It may not hand it out without shrinking the
problem{{if .Full}}; that is the shape that does not terminate, and
the cost people attribute to "multi-agent" is almost always this{{end}}. These instructions give it a
definitive end without capping depth:

- **Every level narrows.** A child's scope is a strict subset of its parent's. A
  worker that would hand on its whole job does the work instead.
- **Every job carries acceptance criteria down.** A child inherits its parent's
  criteria plus its own. A job nobody can evaluate cannot end; that, not the nesting,
  makes depth dangerous.
- **A job that fails its criteria twice is not re-issued.** The root does it locally,
  or serializes it behind whatever keeps breaking it{{if .Full}}. Two jobs
  with an undeclared dependency each break the other's criteria, and re-issuing
  the failing one satisfies every instruction above while alternating forever; the budget
  is what ends it{{end}}.
- **Whatever the parent does not hand out, the parent still owns.** A strict subset
  leaks by construction. Split "no caller of X remains" into per-project jobs, and
  the callers in no project belong to nobody. Every job passes and the goal is
  unmet. Carry a remainder row at each level and close it explicitly.

START EVERY SPAWN AT THE ECONOMY TIER, and SAY which model you picked. Move a job up
only for work the table below assigns higher. The fit still runs both ways: an
ambiguous API boundary does not get the cheapest model because it looked like less
work{{if .Full}}. Judge cost per COMPLETED job: a cheap worker that needs a rerun is not
cheaper{{end}}.

Naming it is the checkable half: every spawn names a model, or an agent definition
that names one.

- Inheriting the parent's model ON PURPOSE is fine and often right{{if .Full}}. A hard review
  under a cheaper coordinator is exactly the case a weaker-only ordering would forbid{{end}}.
- Inheriting it BY OMISSION is the failure. A host defaulting to "same as the parent"
  makes every unnamed spawn the most expensive one, and records no choice{{if .Full}}. Measured 2026-09-15: nine
  workers spawned in one session, every one inheriting the root's model, five of them
  mechanical work a cheaper model does as well{{end}}.

When the tier is unclear, start at economy. A job that fails its criteria there is
re-forked one tier up, not retried at the same one.{{if .Full}} "Only ever spawn something
weaker" was tried and withdrawn, because same-strength offload is legitimate, so this
is a starting point and not a ceiling.{{end}}

Model NAMES belong to the host, never to magus: name your host's model or an agent
definition the user owns. Point a default-subagent setting at the economy model.

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

To prove something, brute-force it at economy. Many economy workers finish about as
fast as one principal worker, for far fewer tokens:

- Fork one read-only job per claim, input, or variant, and run them all at once.
- Spawn the `magus-scout` agent for each where your harness installed it.
- Each reports its command and output ref{{if .Full}}, so evidence settles the claim and not a worker's reading{{end}}.
- The principal tier only reconciles the claims where workers disagree.

Keep economy jobs short-lived:

- The brief points at evidence (paths, output refs, the row) and carries no transcript.
- A worker resends its whole context every turn, and some providers raise a tier's
  price past a prompt size. Where the brief names that size, the worker reports and
  stops before passing it.

Nesting is allowed when the host supports it, but it creates no new budget and no
private ownership map.

- Before a child spawns descendants, it reports the proposed jobs to its parent.
- The store then carries those descendants, their parent, model, criteria, and write
  paths.
- Descendants inherit the ancestor's deny paths and may subdivide only the ancestor's
  write paths.
- A cap the user sets applies to the whole tree, not once per parent.

Keep one integration owner at the root, however deep the job tree.
{{if .Full}}A child may coordinate its descendants, but it may not accept changes
outside its own job, relax top-level acceptance criteria, or hide additional
fan-out from the root. Nest wherever a child has a genuinely separable area and
enough context to partition it better than its parent.{{else}}A child coordinates its descendants but may not relax the root's criteria.{{end}}

## Seed the partition with magus

Choose the target that will validate the work. `ci` is the release gate, but any
target `{{cmd "affected"}} <target>` accepts can be planned:

```sh
magus affected <target> --plan
```

For a proposed change whose paths are known but not yet edited, plan those paths
instead of the current diff:

```sh
printf '%s\n' <repo-relative-path>... | magus affected <target> --stdin --plan
```

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
- Affinity stays with `{{tool "client"}}` (`{{buzz "insight"}}`, read affinity). Without MCP,
  run `{{cmd "buzz"}} -e` printing `magus\insight().affinity`.
- Use `{{cmd "refs"}} <symbol>` when two jobs may touch the same API{{if .Full}}; `{{cmd "path"}} <a> <b>` settles
  a suspicious pair{{end}}.

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
loading for everybody in that checkout at once{{if .Full}}. The rest lose `{{cmd "run"}}`, `{{cmd "ls"}}` and their own tests over an edit they
cannot see. Measured 2026-09-17: three workers
shared a checkout, one saved the root magusfile mid-edit, and all three were
stopped for the duration by a file only one of them had ever opened{{end}}.
When `{{cmd "job fork"}}` refuses one, the answer is a separate worktree, not a narrower
boundary.

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
   magus job fork api-store --model <model> \
     --criteria "accounts move to the new store; nothing names the old one" \
     --write-paths 'api/store.go,api/store_test.go,db/migrations/*.sql' \
     --check "go-test api -- -run TestStore" \
     --checkpoint <checkpoint>
   ```

   Or fork from a record with `{{cmd "job fork"}} --stdin`, the form that carries goals;
   [reference.md](reference.md) shows one.

   `{{cmd "job fork"}} --schema` prints every field and the newest `schema_version`.
3. Spawn the worker with the description `<parent>/<role> <job>`: two words, the
   first a parent id and a role joined by `/`, the second the job id.
   - A root job has no parent row, so its first word takes any label
     (`orchestrator/feat api-store`).
   - A child reads `api-store/fix migrate`; the guard resolves the job as `migrate`,
     else `api-store/migrate`.
   - The role is a word for the work (`feat`, `fix`, `review`). A workspace spawn
     rule may restrict it, and its refusal names the list.

Write paths name FILES or file globs, never a directory. The check is `<target>
<project> [-- args]`, never the gate. [reference.md](reference.md) covers claiming one
declaration in a shared file and forking from a leased worker.

Render the prompt FROM the row; never type it. `{{cmd "describe job"}} <job>` prints the
job's own criteria, boundary and check, plus what the workspace knows and nobody wrote
down{{if .Full}}: the projects the write paths reach, the declared
output globs that land inside them, the paths a sibling job is holding, the build
inputs and workspace configuration that have one owner, and the projects that
change alongside the leased ones without declaring a dependency{{end}}. Two renders of one
row are byte-identical, which hand-typed prompts are not{{if .Full}}: seven of
them disagreed about a dedup key and every one ended with the gate{{end}}.

The guard grades every command a brief presents in a shell fence or a code span. One
it would deny refuses the spawn{{if .Full}}, since the worker runs a brief's
commands as written{{end}}. A line that names a command to forbid it ("never ...") is not
graded.

Every worker prompt includes its row, its JOB ID, relevant graph evidence, and
any cap the user set for the tree. Require the worker to:

- export `BAGGAGE=magus.lease=<its id>` before it works{{if .Full}}: that is the W3C
  Baggage channel, and the member is what tells the agent guard whose declared boundary
  to grade a write against, so a worker that never exports it is graded as an editor
  magus cannot attribute. Export `TRACEPARENT` too when your host has one, and add
  `magus.spawner=<your label>` to the baggage: magus records the trace, the parent span
  and the label as CLAIMS, so `{{cmd "session"}} ls` can show who spawned whom, and no
  verdict is ever keyed on them{{else}}: the guard grades its writes only when that is
  set{{end}};
- preserve unrelated changes, stay inside write paths, and avoid generated outputs;
- run only its assigned magus target.

End every brief with the step that files the result, as the worker's LAST act before
it reports back. A brief that leaves it out gets a prose report and a row that never
moves{{if .Full}}: the store keeps the write paths held, `{{cmd "job wait"}}`
has nothing to verify, and nobody notices until a sibling is refused over a path
the finished worker no longer needs{{end}}. The step, with the shape `{{cmd "job exit"}} --schema`
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

A typed result makes verification mechanical{{if .Full}}; the same four facts in prose can only be
graded by reading, and a worker that ran a filtered subset writes the same
paragraph as one that did not{{end}}. Keep unresolved risks mandatory, so a mis-scoped
worker can say so instead of widening silently.

A worker writes only its own row and the children it forks. Every other store write
is the orchestrator's; a refused worker reports it as an unresolved risk and stops.

After the spawn, the row carries the job:

- A worker records the base it LANDED on, which can differ from the checkpoint you
  handed it.
- A denied worker coordinates and never works around.
- Every row ends in pass, fail, or NO-RETURN, and the root writes which: silence is
  not a pass.

[reference.md](reference.md) covers bases, read paths, releasing a path, moving a live
job's boundary, how magus ends abandoned jobs, and what `{{cmd "job wait"}}` checks.

## Declare the criteria magus can check for you

A GOAL is the part of a job's criteria magus grades itself, from evidence the worker
cannot author. It is a check that passed, or paths or symbols changed, present, absent
or unreferenced. Write goals in the record's `goals`. `{{cmd "job wait"}}` refuses pass until
each verifies. [reference.md](reference.md) has the table and the rename pattern.

## Observe through the correct control plane

Run workers non-blocking; block on one only when your next action needs its result.
Watch processes with `{{cmd "status"}} --watch=15s`. Never message a worker to ask how it
is going: `{{cmd "describe job"}} <job>` grades where it stands, and `{{cmd "job watch"}} <job>`
shows it happening. Message a worker only to CHANGE what it was handed.
[reference.md](reference.md) covers blocked workers and auditing what a job ran.

## Integrate and verify

As jobs finish:

1. Compare the store against the ACTUAL diff since each job's checkpoint, not the
   paths it reported (`{{cmd "graph diff"}} --rev <revision>` for the domain). A differing
   dirty digest means it saw a tree you are not diffing.
2. Run `{{cmd "job wait"}} <job>` on each returned job BEFORE you read its result. It
   checks what is mechanical:
   - every changed path inside the declared write paths and outside the denied ones;
   - a change set that is not empty on a row that writes;
   - the descendants the store carries;
   - the output ref, bound to a run of THAT JOB'S OWN CHECK which the store recorded
     as PASSING{{if .Full}}. There is no `passed` field on the result: wait reads the
     ref's own recorded attempt from the output store and derives the outcome from
     it, never from what the worker claims{{end}}.

   A job that verifies is recorded `pass`. A rejection exits 1 naming every violation;
   a result that will not decode exits 2. A holder does not verify its own job, or a
   sibling's.

   Then reopen the evidence yourself (`{{cmd "query output"}} <ref>`). Wait proves the ref
   names a passing run of this job's own check, never that the work meets the job's
   GOAL. A worker's prose about its criteria is not that evidence.
3. Resolve cross-job API changes centrally; never assign the same seam twice.
4. Regenerate declared outputs once, after source work converges.
5. Re-run `{{cmd "affected"}} <target> --plan` over the actual diff. If its shape
   invalidates the original partition, stop parallel integration and reconcile.
6. Read the integrated changeset with `{{cmd "diff"}} --impact` before landing it.
   {{- if .Full}} It shows
   what the fleet's combined edit reaches, who else has been changing it, and a
   rebuild estimate from recorded run times. It adds what the advisors say and any
   note anchored to a file it touched. Context, never a verdict: nothing gates on it and
   the exit code is unchanged, and a section that is empty means nobody could
   measure it, not that nothing was found.{{else}} It shows reach, other recent
   editors, a rebuild estimate, advisors and anchored notes. It is context, never a
   verdict. An empty section means nobody could measure it, not that nothing was found.{{end}}
7. Run `{{cmd "affected"}} ci` and evaluate the top-level acceptance criteria.

{{if .Full}}Parallelism is an optimization, not the objective. Fewer
well-isolated jobs are usually cheaper than wide fan-out followed by conflict
repair, and the graph is evidence for that judgment rather than permission to
spawn every possible worker.{{else}}Prefer fewer proven-independent jobs over
wide fan-out and conflict repair.{{end}}
