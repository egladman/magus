# Splitting work across agents

Count the WRITE SETS your change needs (the distinct groups of files to edit), not the
projects it invalidates. Getting this backwards is the standard fan-out failure{{if .Full}}: a
one-line edit in a central package invalidates half the workspace and is still one
edit, and fanning it out produces several agents editing one file{{else}}. A
one-line edit in a central package invalidates half the workspace and is still one
edit{{end}}.

`magus affected <target> --plan` partitions VALIDATION: which targets to run, grouped
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

Acceptance evidence is an output ref the root reopens (`magus query output <ref>`),
never a worker's prose. A worker that ran a filtered subset, or quietly restated its
criteria into something it did pass, reports success either way, and a
transcript cannot tell you which happened.

An agent may report that its edits are done, but no job is complete until its
acceptance criteria and assigned check pass. The root agent, not a worker,
decides whether the top-level goal is complete.{{else}}The loop: define, partition, hand out, observe, evaluate, course-correct, integrate.

- Acceptance evidence is an output ref the root reopens (`magus query output <ref>`),
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

Check each declared name with `magus refs` before forking. A bare name that resolves
to more than one definition is graded against one of them, silently.

This applies with zero workers. A solo change across several call sites is a one-job
tree. Skipping the declaration because nothing is forked is the same failure{{if .Full}} without
a fork to blame. Write the table in the
conversation, check it with `magus refs` when the edits land, and only then report
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
  triggers. Read the live pool (`magus status`) before starting a validation, not
  before starting an agent. Serialize validations that share a worktree, even when
  their write sets are disjoint.

Assign the check from the pipeline the workspace composed, not from convention.
`magus describe target ci <project>` names what `ci` chains and in what order.

- A job gets the narrowest target from that decomposition.
- The integrator re-runs the described order, and `magus affected ci` re-proves the
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
  "done" with nothing executed, which `magus job wait` already refuses to record
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

Pick the model that FITS the job, and SAY which one. The fit runs both ways. A
mechanical rename does not need the strongest model. An ambiguous API boundary
does not get the cheapest one because it looked like less work{{if .Full}}. Matching the model to the work is the only cost decision worth making here;
past that, cost is not your call to agonize over, and a job done badly by an
under-powered worker costs more than the model it saved{{end}}.

Naming it is the checkable half: every spawn names a model, or an agent definition
that names one.

- Inheriting the parent's model ON PURPOSE is fine and often right{{if .Full}}. A hard review
  under a cheaper coordinator is exactly the case a weaker-only ordering would forbid{{end}}.
- Inheriting it BY OMISSION is the failure. A host defaulting to "same as the parent"
  makes every unnamed spawn the most expensive one, and records no choice{{if .Full}}. Measured 2026-09-15: nine
  workers spawned in one session, every one inheriting the root's model, five of them
  mechanical work a cheaper model does as well{{end}}.

ASK THE HUMAN when the right model is unclear, before spawning.{{if .Full}} There is no ordering to fall back on: "only ever spawn something
weaker" was tried and withdrawn, because same-strength offload is legitimate. An
unclear case is a question, not a default.{{end}}

Model NAMES belong to the host, never to magus. Name the model your host names, or an
agent definition the user owns.{{if .Full}} Where the host has a default-subagent setting,
setting it makes omission cheap instead of expensive.{{end}}

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
{{if .Full}}A child may coordinate its descendants, but it may not accept changes
outside its own job, relax top-level acceptance criteria, or hide additional
fan-out from the root. Nest wherever a child has a genuinely separable area and
enough context to partition it better than its parent.{{else}}A child coordinates its descendants but may not relax the root's criteria.{{end}}

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
  run `magus buzz -e` printing `magus\insight().affinity`.
- Use `magus refs <symbol>` when two jobs may touch the same API{{if .Full}}; `magus path <a> <b>` settles
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
loading for everybody in that checkout at once{{if .Full}}. The rest lose `magus run`, `magus ls` and their own tests over an edit they
cannot see. Measured 2026-09-17: three workers
shared a checkout, one saved the root magusfile mid-edit, and all three were
stopped for the duration by a file only one of them had ever opened{{end}}.
When `{{cmd "job fork"}}` refuses one, the answer is a separate worktree, not a narrower
boundary.

Fork records what it could prove in `write_proof` (`alone`, `disjoint` or
`overlapping`), and `magus ls jobs` prints it. An overlap is recorded, not refused:
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

   Or fork from a record with `magus job fork --stdin`, the form that carries goals
   (see the next section).{{if .Full}} For example:

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

   {{- end}}

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
matches one (`api`, `internal/**`) is refused with {{mgslink "MGS3018"}}{{if .Full}}, because it
claims every file under it and so overlaps every job editing anything there{{end}}.

Two jobs share one file by claiming declarations in it: `<file>#<declaration>`, such
as `internal/agent/catalog.go#SkillVersion`.

- A claim names one file, never a glob.
- The file needs a diff driver (`magus doctor` lists the managed ones); otherwise
  fork refuses with {{mgslink "MGS3031"}}.
- Two jobs that must edit the SAME declaration still have one owner. Give it to one
  job, or order them with `--depends-on`{{if .Full}}. Claiming the whole file, or a glob over its directory, is
  what serializes every other job that needed one function in it{{end}}.

The check is `<target> <project> [-- args]` with the `magus run` implied (`"test ."`,
`"go-test api -- -run TestStore"`), never the gate or a target that chains to it.

Fork with `{{tool "client"}}` (`{{buzz "job.put"}}`) from an agent, or `magus job fork` from a
terminal{{if .Full}}: the same store and the same authorization either way, so a
job forked by hand and one an agent forked are indistinguishable to everything that
reads them{{end}}. A worker holding a lease forks its own units the same way, naming its
job with `--parent` and paths inside its own.
{{if .Full}}
The same checkpoint is what a later incremental re-review diffs from (see the
{{skill "change-summary"}} skill); review time and pickup time read the same object.
{{end}}
Render the prompt FROM the row; never type it. `magus describe job <job>` prints the
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
  and the label as CLAIMS, so `magus session ls` can show who spawned whom, and no
  verdict is ever keyed on them{{else}}: the guard grades its writes only when that is
  set{{end}};
- preserve unrelated changes, stay inside write paths, and avoid generated outputs;
- run only its assigned magus target.

End every brief with the step that files the result, as the worker's LAST act before
it reports back. A brief that leaves it out gets a prose report and a row that never
moves{{if .Full}}: the store keeps the write paths held, `magus job wait`
has nothing to verify, and nobody notices until a sibling is refused over a path
the finished worker no longer needs{{end}}. The step, with the shape `magus job exit --schema`
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

The checkpoint you recorded is what you HANDED the job; the base it LANDED ON is a
separate fact. Hosts that isolate workers in per-worker trees routinely branch them
from an older revision than the tree you partitioned{{if .Full}}, and every diff-since-checkpoint in Integrate and verify
silently lies when the recorded base is not the real one{{end}}.

- A worker whose spawn title names its job (`<parent>/<role> <job>`) is bound and
  records its base on its first call. Any other worker runs `magus job exec <its id>`
  once to record it.
- The guard binds the caller that ran it, keyed on the host's session and subagent
  ids. Workers sharing a tree each hold their own lease, and none binds you{{if .Full}}. Nothing to pass: the
  CLI cannot tell a subagent from its parent, which is why the binding is the
  guard's, and a host that names neither id binds the checkout for every caller like
  it{{end}}.
- The answer is a status on the row: match, revision-match (same revision, different
  uncommitted patch), diverged, or unknown. A reading names both tokens and the next
  step.
- It is a FACT, not a gate: every status records, diverged included{{if .Full}}, because refusing would leave the orchestrator
  with no record that a worker went to the wrong base, which is the one case the
  record exists for{{end}}.
- Acting on it is yours. Respawn from the right revision. Or have the worker
  materialize the files it builds on from the intended one, and re-fork the job
  with the right checkpoint. Materialize with `git show <rev>:<path> > <path>`, verifying
  each blob against `git rev-parse <rev>:<path>`{{if .Full}}. A worker
  that edits stale content without noticing reports clean validation against a tree
  nobody ever merges{{end}}.

Name any fact that will READ as drift to the worker's snapshot (a project deleted
this session, a rename, an index regenerated underneath it). Never write a generic
"expect drift" line{{if .Full}}, which only primes the worker to dismiss real
anomalies: the specific fact is what keeps unexplained tree state from costing
an investigation or a helpful revert of something correct{{else}}; it primes the
worker to dismiss real anomalies{{end}}.

Write paths bound READS too: a worker may read its projects and what they declare
`depends_on`. When a worker must READ something it must not WRITE, put that path in the
row's `read_paths` instead of widening `write_paths`{{if .Full}}: one list cannot say
both, and widening the write paths to open a read is how two workers end up owning
one file{{else}}. Widening is how two workers end up owning one file{{end}}.

Ownership ends when EDITING ends, not when the worker exits. A worker done writing a
contested path announces the release at once, then carries on validating. It shrinks
the job's `write_paths` with another `{{tool "client"}}` (`{{buzz "job.put"}}`) write, or messages
the orchestrator if the host supports it{{if .Full}}. A waiting job
starts against the released file while the first is still running tests, which
is most of a worker's lifetime; holding every path to exit serializes agents on
time they spend not editing{{end}}.

That write records each dropped path with its digest at that moment. Hand the digest
to the job taking the path over. One that no longer matches at verification means the
waiter built on a tree the releaser never saw.

Moving a live job's boundary is yours. Change its record and run `magus job apply -f
<file>` (`-f -` reads stdin).

- The record is the whole spec: one write widens or revokes write paths and adds
  goals, and the job keeps its state{{if .Full}}. A re-fork would hand a taken job out again as
  declared{{end}}.
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
- the checkout `{{cmd "job exec"}}` took it in no longer exists;
- it is still `declared`, nobody ever took it, and it was not updated within
  `jobs.stale_after` (default 2h, `0` for never){{if .Full}}. A root outlives children still working under it;
  the guard noting somebody else's write in a job's paths does not count as an
  update{{end}}.

Each ended job prints `ended <id>: <reason>` on stderr. So remove a worker's worktree
only once its job is done, and advance a root you are still using. What magus cannot
prove it leaves live, and ending those stays yours. A taken job that went quiet reads
`stale`, one past its timeout `overdue`, each with `{{cmd "job exit"}} <id>`.

`--timeout <duration>` on fork is OPTIONAL and unset by default. Past it the guard
denies every write graded under that lease, and its paths stop blocking other jobs.
The row stays live until you end it. A job with acceptance criteria needs no bound{{if .Full}}: the goals end it, and a timeout only helps where a stuck worker
would otherwise hold paths nobody else can write{{end}}.

`{{cmd "job wait"}}` holds a writing job's `changed_paths` to the diff magus observes since
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
re-partition, or have the owning job release the path, then retry{{if .Full}}. Editing
anyway from an un-enrolled shell, or dropping the lease id to buy advisory
treatment, turns a denial you could have acted on into a collision nobody sees
until integration{{end}}. Step 1 of Integrate and verify checks the same boundary against
the checkpoint: the half that does not depend on a worker cooperating.

A read-only job carries an abbreviated row: no write paths, no deny paths. Every row
ends in pass, fail, or NO-RETURN, and the root writes which{{if .Full}}: silence
is not a pass, and a worker that dies, stalls, or is killed is a different state
from one that failed its criteria{{else}}: silence is not a pass{{end}}.

{{if .Full}}Acceptance criteria must be observable. Prefer named tests, generated
artifacts, diagnostics, API behavior, or specific review checks over phrases such
as "works correctly." A child that hands work on remains responsible for evaluating
its descendants before reporting upward. The root still verifies the combined
result independently.{{else}}Make acceptance criteria observable: named tests, artifacts, diagnostics, API
behavior, or review checks. A child that hands work on evaluates its descendants
before reporting upward.{{end}}

## Declare the criteria magus can check for you

A job's acceptance criteria are prose a reader grades. A GOAL is the part magus grades
itself, from evidence the worker cannot author. `magus job wait` refuses to record pass
until every one verifies.

- Goals are data: write them in the job record's `goals`, never as flags.
- `magus job fork` refuses a job that writes and declares neither a check nor a goal.
- `{{tool "client"}}` (`{{buzz "job.put"}}`) takes the same `goals` array.{{if .Full}}
- The `--stdin` record above declares a `paths` goal and two `symbol` goals.{{end}}

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
attestation. There is no goal for "this command exited 0"{{if .Full}}, deliberately: magus
did not record that run and cannot attribute it, so it would be the easiest goal to
satisfy falsely{{end}}. Declare a target and use `check`.

The worker's own `changed_paths` is its account, never the evidence. The diff and the
tree are read in the checkout that took the job, so wait from your own tree while the
worker's still exists. An observation magus could not MAKE fails the goal{{if .Full}}: otherwise the cheapest way past
a goal would be to break the observation{{end}}.

Ask where a job stands without advancing it:

```sh
magus describe job <job>
```

It prints where each goal stands beside the terms: the same grading `magus job wait`
does, recording nothing. Use it instead of asking a worker how it is going.

SEQUENCE goals with `depends_on` between them; a failed prerequisite propagates. Do
NOT nest them: a goal that wants children is a JOB that wants splitting{{if .Full}}, which the instructions above cover. Flat goals keep
the job tree the only hierarchy with an owner{{end}}.

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
tool{{if .Full}}: a shell loop holds your tool slot for the whole wait{{end}}.

### "How is it going" is a read, never a message

Never message a worker to ask how it is doing. The question costs it the turn it was
in, and what comes back is its account of itself, not what happened{{if .Full}}. Three reads answer it and none of them needs the
worker's cooperation: a changed file is the filesystem reporting a fact, a tool
call is what the guard already recorded, and a gate is graded against evidence
magus is holding anyway{{else}}. Three reads answer it without touching the
worker{{end}}.

| you want | read |
| --- | --- |
| to hand the question to a person | the console link every verb that names a job prints |
| to watch it happen | `{{cmd "job watch"}} <job>` |
| to know whether it is finished | `{{cmd "describe job"}} <job>` |

`{{cmd "job watch"}}` prints one line per event until you interrupt it. It merges files
changed under the job's write paths, the guard's tool calls under its lease, and the
runs recorded against it. A file is attributed by WRITE PATH and by nothing the worker says{{if .Full}}: the write paths are proven disjoint when the job forks, so the
path alone names the holder. Where two live jobs do cover one path the line says
`contested`, names both, and attributes it to neither; there is nothing in a path
to break that tie with, and naming one would tell you a file moved under a worker
that never touched it{{else}}, so the worker cannot make it quiet{{end}}.

Message a worker only to CHANGE what it was handed. Anything you merely want to KNOW
is one of the three reads above.

A blocked worker RAISES; it never stalls quietly.

- Piping the block to `magus session notify --outcome waiting` (blocked on input) or
  `--outcome permission` (blocked on approval) opens a durable request in this
  repository. No other outcome opens one.
- `magus session attention` lists what is open{{if .Full}}, keyed by repository identity rather
  than by checkout path, so a request raised inside a worker's own isolated tree is
  listed in yours{{end}}. `magus session attention -q` prints nothing and exits 1 on an empty
  queue: the form to test from a loop.
- Nothing closes a request by itself. The orchestrator, or any human, disposes it
  with `magus session dispose <id> --reason "<why>"`{{if .Full}}. There is no expiry and no auto-dispose, because a
  request magus could answer on its own would not have needed a person{{end}}.
- A worker that raised one waits for the disposition; it never chooses for itself.

`magus session` is how the root audits what a job RAN, as opposed to what it reported.
`magus session --since 2h -o json` answers what the fleet has been doing.

{{if .Full}}Each invocation carries the job it was launched under (the same `magus.lease` channel).
It also carries its claimed spawner label and parent span, and the targets it finished
with their outcomes. The store is keyed by repository identity, so a worker in its own
worktree is still listed.
Attribution is cooperative and every one of those values is a CLAIM magus records
rather than corroborates: an empty lease means the invocation claimed none, which
makes it unattributed, never an error; its OS user says whose account ran it.{{else}}Each
invocation carries its lease, its claimed spawner and parent span, and the targets
it ran with their outcomes. A worker in its own worktree is still listed.{{end}}

{{if .Full}}Course-correct at explicit checkpoints: after a child proposes new
descendants, when a worker discovers a new API or generated-output dependency,
when ownership drifts, when criteria repeatedly fail, and when status shows
unexpected lock contention or service failure. Pause only the affected branch,
update the jobs and ordering, then resume work that remains independent. Never
guess a PID or signal from stale output; use current status and the host's normal
process controls.{{else}}Re-plan when nesting, dependencies, ownership, failing criteria, locks, or
services change. Update the jobs before resuming affected work, and never act on a
guessed or stale PID.{{end}} A running worker keeps the constraints it was handed.
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
     as PASSING{{if .Full}}. There is no `passed` field on the result: wait reads the
     ref's own recorded attempt from the output store and derives the outcome from
     it, never from what the worker claims{{end}}.

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
6. Read the integrated changeset with `magus diff --impact` before landing it.
   {{- if .Full}} It shows
   what the fleet's combined edit reaches, who else has been changing it, and a
   rebuild estimate from recorded run times. It adds what the advisors say and any
   note anchored to a file it touched. Context, never a verdict: nothing gates on it and
   the exit code is unchanged, and a section that is empty means nobody could
   measure it, not that nothing was found.{{else}} It shows reach, other recent
   editors, a rebuild estimate, advisors and anchored notes. It is context, never a
   verdict. An empty section means nobody could measure it, not that nothing was found.{{end}}
7. Run `magus affected ci` and evaluate the top-level acceptance criteria.

{{if .Full}}Parallelism is an optimization, not the objective. Fewer
well-isolated jobs are usually cheaper than wide fan-out followed by conflict
repair, and the graph is evidence for that judgment rather than permission to
spawn every possible worker.{{else}}Prefer fewer proven-independent jobs over
wide fan-out and conflict repair.{{end}}
