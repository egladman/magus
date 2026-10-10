# magus-multi-agent reference

The detail behind the magus-multi-agent skill: what a job record may claim, what happens to a job after its spawn, the goals magus grades, and how to watch the fleet without interrupting it.

## Write paths, claims and checks

Write paths name FILES: a file, a file the job creates, or a file glob
(`internal/queue/*.go`, `docs/**/*.md`). A directory, a project root, or a glob that
matches one (`api`, `internal/**`) is refused with {{mgslink "MGS3018"}}{{if .Full}}, because it
claims every file under it and so overlaps every job editing anything there{{end}}.

Two jobs share one file by claiming declarations in it: `<file>#<declaration>`, such
as `internal/agent/catalog.go#SkillVersion`.

- A claim names one file, never a glob.
- The file needs a diff driver (`{{cmd "doctor"}}` lists the managed ones); otherwise
  fork refuses with {{mgslink "MGS3031"}}.
- Two jobs that must edit the SAME declaration still have one owner. Give it to one
  job, or order them with `--depends-on`{{if .Full}}. Claiming the whole file, or a glob over its directory, is
  what serializes every other job that needed one function in it{{end}}.

The check is `<target> <project> [-- args]` with the `{{cmd "run"}}` implied (`"test ."`,
`"go-test api -- -run TestStore"`), never the gate or a target that chains to it.

Fork with `{{tool "client"}}` (`{{buzz "job.put"}}`) from an agent, or `{{cmd "job fork"}}` from a
terminal{{if .Full}}: the same store and the same authorization either way, so a
job forked by hand and one an agent forked are indistinguishable to everything that
reads them{{end}}. A worker holding a lease forks its own units the same way, naming its
job with `--parent` and paths inside its own.
{{if .Full}}
The same checkpoint is what a later incremental re-review diffs from (see the
{{skill "change-summary"}} skill); review time and pickup time read the same object.
{{end}}

## After the spawn

The checkpoint you recorded is what you HANDED the job; the base it LANDED ON is a
separate fact. Hosts that isolate workers in per-worker trees routinely branch them
from an older revision than the tree you partitioned{{if .Full}}, and every diff-since-checkpoint in Integrate and verify
silently lies when the recorded base is not the real one{{end}}.

- A worker whose spawn title names its job (`<parent>/<role> <job>`) is bound and
  records its base on its first call. Any other worker runs `{{cmd "job exec"}} <its id>`
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

Moving a live job's boundary is yours. Change its record and run `{{cmd "job apply"}} -f
<file>` (`-f -` reads stdin).

- The record is the whole spec: one write widens or revokes write paths and adds
  goals, and the job keeps its state{{if .Full}}. A re-fork would hand a taken job out again as
  declared{{end}}.
- `--dry-run` prints the spec diff and writes nothing.
- A check you find the job owes mid-flight is a goal you add this way, so
  `{{cmd "job wait"}}` grades it from a recorded run.
- A revoked path is recorded as a release with its digest. The worker's next write
  there is refused, naming the revocation.
- Ending a whole job stays `{{cmd "job exit"}} <job>`. A worker never widens: its refusal
  names `{{cmd "describe job"}}` and tells it to ask you.

Advance the row on every state change. `{{cmd "ls jobs"}}` then shows which live jobs
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
itself, from evidence the worker cannot author. `{{cmd "job wait"}}` refuses to record pass
until every one verifies.

- Goals are data: write them in the job record's `goals`, never as flags.
- `{{cmd "job fork"}}` refuses a job that writes and declares neither a check nor a goal.
- `{{tool "client"}}` (`{{buzz "job.put"}}`) takes the same `goals` array.

A record on stdin declares a `paths` goal and two `symbol` goals:

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

It prints where each goal stands beside the terms: the same grading `{{cmd "job wait"}}`
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

- Piping the block to `{{cmd "session notify"}} --outcome waiting` (blocked on input) or
  `--outcome permission` (blocked on approval) opens a durable request in this
  repository. No other outcome opens one.
- `{{cmd "session attention"}}` lists what is open{{if .Full}}, keyed by repository identity rather
  than by checkout path, so a request raised inside a worker's own isolated tree is
  listed in yours{{end}}. `{{cmd "session attention"}} -q` prints nothing and exits 1 on an empty
  queue: the form to test from a loop.
- Nothing closes a request by itself. The orchestrator, or any human, disposes it
  with `{{cmd "session dispose"}} <id> --reason "<why>"`{{if .Full}}. There is no expiry and no auto-dispose, because a
  request magus could answer on its own would not have needed a person{{end}}.
- A worker that raised one waits for the disposition; it never chooses for itself.

`{{cmd "session"}}` is how the root audits what a job RAN, as opposed to what it reported.
`{{cmd "session"}} --since 2h -o json` answers what the fleet has been doing.

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

