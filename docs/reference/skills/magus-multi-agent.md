---
title: magus-multi-agent
generated_from: internal/agent/skills/magus-multi-agent/SKILL.md
description: "Split work across agents in a magus workspace as an acceptance-criteria loop: partition by WRITE SET using graph evidence (magus refs --occurrences, explain, affected --plan --stdin), prove the leases cannot collide, bound fan-out depth, and match each lease's model to the work it needs."
tags: [agents, skills, magus-multi-agent]
aliases:
  - reference/skills/magus-delegate-ultra
  - reference/skills/magus-delegate-multi-agent
skill_full_bytes: 21840
skill_simple_bytes: 16102
---

# magus-multi-agent

Split work across agents in a magus workspace as an acceptance-criteria loop: partition by WRITE SET using graph evidence (magus refs --occurrences, explain, affected --plan --stdin), prove the leases cannot collide, bound fan-out depth, and match each lease's model to the work it needs. Use when a change needs several disjoint groups of files edited, when an audit or review covers a tree, or when the user says "fan this out" or "spin up an agent per package" - you do not need to be asked. Do NOT fan out one coherent edit just because it invalidates many projects: a shard plan partitions VALIDATION, not editing, so it can veto a fan-out but never license one.

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
| `agent-skill-version` | `48` |
| `knowledge-schema-version` | `10` |
| `skill-content` | `9e89dfcffbde` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both permutations below report it: they go stale together, never one silently, and a change to another skill does not move it.

## Full form

Every mechanical step spelled out, plus the rationale for each. Installed as the `<name>-full` twin: loaded by name rather than always, so a reader who needs the long form can ask for it without every session carrying it.

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
workers that hand work on without a shrinking scope, leases with no acceptance criteria
so nobody can say when to stop, and a principal tier assigned to mechanical edits.
Each of those is a choice made below, not a property of fanning out. Say what a
round will cost when the user is deciding, and prefer the smallest fan-out that
covers the work.

Fan out only after the collision check below REPORTS the leases disjoint; write
sets that look separate is not that check. If the graph supports only one
coherent write set, keep the work local - fanning out one lease adds coordination
and buys nothing. The root agent owns the goal, the budget, the topology,
integration, and final verification, and never hands those out.

## Run the graph-engineering loop

Graph engineering is a natural evolution of loop engineering. The
human supplies a goal and constraints; the root agent turns them into explicit
acceptance criteria, uses the knowledge graph to partition the work, hands out
bounded prompts, observes results, evaluates them against the criteria, and
course-corrects until the integrated goal is satisfied. The graph improves the
loop's partition and collision decisions; it does not replace the loop.

Run this control loop:

1. State the top-level goal, constraints, and observable acceptance criteria.
2. Map the affected graph and propose collision-resistant edit leases.
3. Give every lease its own goal, ownership boundary, and acceptance criteria.
4. Hand out work within one global cost and concurrency budget.
5. Observe agents and Magus processes through their separate control planes.
6. Evaluate evidence, revise ownership or ordering when assumptions change, and
   repeat until the criteria pass.
7. Integrate centrally and run the release gate.

Acceptance evidence is an output ref the root reopens (`magus query output <ref>`),
never a worker's prose. A worker that ran a filtered subset, or quietly restated its
criteria into something it did pass, reports success either way - and a
transcript cannot tell you which happened.

An agent may report that its edits are done, but no lease is complete until its
acceptance criteria and assigned validation pass. The root agent, not a worker,
decides whether the top-level goal is complete.

## Set one topology boundary

Before spawning, state one compact budget: maximum simultaneously active agents,
effort tier per lease, whether isolated worktrees are available, and how deep
leases may nest. Editing costs the workspace nothing; what contends is
VALIDATION - the magus runs a lease triggers - so size that cap from the live
pool (`magus status`) rather than a fixed number, and serialize validations
that share a worktree even when their write sets are disjoint. Ask before
exceeding the budget.

Assign validation from the pipeline the workspace composed, not from convention:
`magus describe target ci <project>` names what `ci` chains and in what order,
so a lease gets the narrowest target from that decomposition and the integrator
re-runs the described order, with `magus affected ci` re-proving the whole
composition. A worker hand-sequencing lint, format, and test is re-deriving an
order the magusfile already owns, and the step it forgets fails silently by
omission.

Decide each lease's validation PLANE with its target. A worker environment that
cannot execute magus at all - an isolated tree with no usable binary, or a
guard that routes raw language tools back to targets it cannot run - cannot
validate anything it writes. Mark that lease's validation ROOT-DEFERRED in the
ledger before spawning: the worker writes the tests, stops at the static checks
its environment does run, and says so; the root executes the lease's target
centrally before accepting. Leaving each worker to discover the wall
spends its budget on the discovery, once per worker, and its report then reads
"done" with nothing executed - which the acceptance-evidence rule above already
refuses to accept.

A worker may hand work on again. What it may not do is hand it on without shrinking the
problem - that is the shape that does not terminate, and the cost people
attribute to "multi-agent" is almost always this. Three rules give it a
definitive end:

- **Every level narrows.** A child's scope is a strict subset of its parent's. A
  worker that would hand on its whole lease should do the work instead.
- **Depth is capped.** Two levels below the root by default: the root hands out leases,
  a lease may hand out parts of its own, and those parts do the work. Deeper than that
  and the root can no longer say what is running or why. Say so if you need more.
- **Every lease carries acceptance criteria down with it.** A child inherits its
  parent's criteria plus its own. A lease nobody can evaluate is a lease that cannot
  end, which is what makes depth dangerous rather than the nesting itself.
- **A lease that fails its criteria twice is not re-issued.** The root does it
  locally, or serializes it behind whatever keeps breaking it. Two leases
  with an undeclared dependency each break the other's criteria, and re-issuing
  the failing one satisfies every rule above while alternating forever; the budget
  is what ends it.
- **Whatever the parent does not hand out, the parent still owns.** A strict subset
  leaks by construction: split "no caller of X remains" into per-project leases and
  the callers in no project belong to nobody, so every lease passes and the goal is
  unmet. Carry a remainder row at each level and close it explicitly.

Pick the model that FITS the lease. That is the whole rule, and it runs both ways:
a mechanical rename does not need the strongest model available, and an ambiguous
API boundary does not get the cheapest one because it looked like less work.
Matching the tier to the work is the only cost decision worth making here - past
that, cost is not your call to agonize over, and a lease done badly by an
under-powered worker costs more than the model it saved.

Map work to provider capabilities without assuming model names:

| Tier | Assign |
|---|---|
| principal | architecture, ambiguous ownership, public APIs, migrations, security, integration |
| standard | isolated implementation with a clear contract and bounded project surface |
| economy | mechanical edits, fixtures, docs, inventory, and read-only evidence gathering |

If the host cannot select models or reasoning effort, keep its default. Tool surface
is a separate axis from tier: evidence gathering, scouting, and review get a
read-only tool surface where the host offers one. Never downgrade the root
integration pass or final release gate.

Nesting is allowed when the host supports it, but it does not create a
new budget or a private ownership map. Before a child spawns descendants, it must
report the proposed leases to its parent. The root ledger must then record those
descendants, their parent, effort tier, criteria, and owned paths. Descendants
inherit the ancestor's forbidden paths and may subdivide only the ancestor's
owned paths. Apply worker and cost caps globally, not once per parent.

Keep one integration owner at the root even when the lease tree is deep.
A child may coordinate its descendants, but it may not accept changes
outside its own lease, relax top-level acceptance criteria, or hide additional
fan-out from the root. Prefer a shallow tree unless a child has a genuinely
separable area and enough context to partition it better than the root.

## Seed the partition with Magus

Choose the target that will validate the work. `ci` is the release gate, but any
target accepted by `magus affected <target>` can be planned:

```sh
magus affected <target> --plan --max-shards <global-worker-cap>
```

For a proposed change whose paths are known but not edited yet, plan those paths
instead of the current diff:

```sh
printf '%s\n' <repo-relative-path>... | magus affected <target> --stdin --plan --max-shards <global-worker-cap>
```

Read the JSON fields `count`, `max_parallel`, `source`, and `matrix`. A shard is a
history-balanced execution group, not an edit assignment or proof of write
isolation. Use it only as the first partition. If paths are not known, query the
task's symbols, files, and projects before producing a stdin plan.

## Prove that leases do not collide

Classify the union of every lease's proposed paths in one call:

```sh
magus describe file <both leases' paths>... -o json
```

Read the facts: `overlaps` lists each declaration covering more than one
proposed path - a shared write set by construction; `claims[].target` names the
target that regenerates a path (generated outputs have one integration owner,
never hand-edited by workers); `depends_on` carries the owner's direct edges.
Affinity stays with `magus_insight lens=affinity`, and `magus refs <symbol>`
when two leases may touch the same API; `magus path <a> <b>` settles
a suspicious pair. A read-only lease has no write set, so it is outside
this analysis entirely.

Two leases may run together only when:

- The combined classification reports no overlaps - source write sets and
  declared outputs disjoint.
- Neither consumes an API or generated artifact the other will change.
- Shared manifests, lockfiles, schemas, workspace configuration, and agent
  instructions have one owner.
- Dependency and temporal-affinity evidence does not indicate that they should
  move together.

Project boundaries alone are insufficient. A declared dependency means group the
work or serialize producer before consumer. Treat strong hidden affinity as a
warning. When evidence is incomplete, reduce parallelism.

## Maintain the global lease ledger

Before spawning, record one row per lease - including the checkpoint it was handed
(`magus vcs checkpoint -o name`: the revision, plus a dirty-patch digest when the
tree is not clean) - and keep descendants in the same table:

The same checkpoint is what a later incremental re-review diffs from (see the
magus-change-summary skill) - review time and handoff time read the same object.

| Lease | Parent | Checkpoint | Goal and acceptance criteria | Owned paths | Forbidden paths | Depends on | Tier | Validation | State |
|---|---|---|---|---|---|---|---|---|---|

Every worker prompt must include its row, its LEASE ID, relevant graph
evidence, and the global spawn rule. Require the worker to export
`BAGGAGE=magus.lease=<its id>` before it works - that is the W3C
Baggage channel, and the member is what tells the agent guard whose declared boundary
to grade a write against, so a worker that never exports it is graded as an editor
magus cannot attribute. Export `TRACEPARENT` too when your host has one, and add
`magus.spawner=<your label>` to the baggage: magus records the trace, the parent span
and the label as CLAIMS, so `magus session ls` can show who spawned whom, and no
verdict is ever keyed on them. Require it to preserve
unrelated changes, stay inside owned paths, avoid generated outputs, run only its
assigned Magus target, and return changed paths, validation evidence, descendants
it created, and unresolved risks.

The checkpoint you recorded is what you HANDED the lease; the base it
actually LANDED ON is a separate fact, because hosts that isolate workers in
per-worker trees routinely branch them from an older revision than the tree you
partitioned - and every diff-since-checkpoint in Integrate and verify
silently lies when the recorded base is not the real one. A worker's first
required act is registering it: run `magus vcs checkpoint -o name` in ITS OWN tree
and call `magus_ledger op=register id=<its row> reported_base=<that token>`. The
answer is a verdict recorded on the row - match, revision-match (same revision,
different uncommitted patch), diverged, or unknown - plus a reading of it that
names both tokens and the next step. It is a FACT and not a gate: every verdict
registers, diverged included, because refusing would leave the orchestrator
with no record that a worker went to the wrong base, which is the one case the
record exists for. Acting on it is still yours: respawn from the right
revision, or have the worker materialize the files it builds on from the intended
one (`git show <rev>:<path> > <path>`, verifying each blob against
`git rev-parse <rev>:<path>`) and re-put the row's checkpoint. A worker
that edits stale content without noticing reports clean validation against a tree
nobody will ever merge.
Also name any fact that will READ as drift to the worker's snapshot - a project
deleted this session, a rename, an index regenerated underneath it - never a
generic "expect drift" line, which only primes the worker to dismiss real
anomalies: the specific fact is what keeps unexplained tree state from costing
an investigation or a helpful revert of something correct.

Ownership ends when EDITING ends, not when the worker exits. A worker that has
finished writing a contested path announces the release immediately - shrink the
lease's `owned_paths` with another `magus_ledger` put, or message the orchestrator
if the host supports it - and then carries on validating. A waiting lease
starts against the released file while the first is still running tests, which
is most of a worker's lifetime; holding every path to exit serializes agents on
time they spend not editing.

That put records each dropped path with the digest it carried at that moment.
Hand the digest to the lease taking the path over: it names the version being
inherited, and one that no longer matches at verification means the waiter built
on a tree the releaser never saw.

Re-put the row on every state change. `op=list` then answers two questions you
would otherwise derive by hand: which live leases claim intersecting
`owned_paths`, and how long since each row was touched. Both are facts, not
verdicts - magus transitions nothing, so a row that has gone quiet is a lease YOU
decide is possibly dead, and a reported overlap is a pair you either intended or
must repartition.

The ledger RECORDS and the agent guard GRADES. A worker that exported
`magus.lease` has each file write judged against these declarations as it
happens: inside its own owned paths passes; inside its forbidden paths, or inside
another live lease's owned paths, is DENIED, and the denial names the owning
lease. A writer magus cannot attribute - a person in their own checkout, or a
worker that never enrolled - is ADVISED and never blocked, and every uncertainty
fails open the same way: no ledger, no live leases, a ledger file
that will not parse. It is a seatbelt for harnesses that opt in, not a
sandbox. So a denied worker
COORDINATES and never works around: ask the orchestrator to re-partition, or have
the owning lease release the path with the `owned_paths` put above once it has
finished editing, then retry. Editing anyway from an un-enrolled shell, or
dropping the lease id to buy advisory treatment, turns a denial you could have
acted on into a collision nobody sees until integration. Step 1 of Integrate
and verify checks the same boundary against the checkpoint, and that is the half
that does not depend on a worker cooperating.

A read-only lease carries an abbreviated row: no Owned paths, no Forbidden paths. Every
row ends in pass, fail, or NO-RETURN, and the root writes which: silence
is not a pass, and a worker that dies, stalls, or is killed is a different state
from one that failed its criteria.

Acceptance criteria must be observable. Prefer named tests, generated
artifacts, diagnostics, API behavior, or specific review checks over phrases such
as "works correctly." A child that hands work on remains responsible for evaluating
its descendants before reporting upward. The root still verifies the combined
result independently.

Run workers non-blocking by default, and block on one only when your next action
requires its result. An agent spawned merely to wait, poll, or repeat discovery the
root already owns is not an edit lease and spends budget for nothing.

## Observe through the correct control plane

Use the provider's agent or task view to track the lease tree, agent state,
messages, and completion. Use this to keep the root ledger aware of descendants.

Use Magus to watch processes and shared workspace resources:

```sh
magus status --watch=15s
```

This shows Magus process state, lock holders and waiters, and shared-service
state and adoption. It does not show an agent that is thinking without running a
Magus process. Do not replace it with sleep loops, repeated `ps`, or a waiting
agent.

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

`magus session` is how the root audits what a lease actually RAN, as opposed
to what it reported. Each session carries the lease it was launched under -
the same `magus.lease` channel - along with the spawner label and parent span it
claimed, the targets it finished and how they ended, and the store is keyed by
repository identity, so a worker in its own worktree is still listed here.
Attribution is cooperative and every one of those values is a CLAIM magus records
rather than corroborates: an empty lease means the session claimed none, which is
the ordinary answer for anything a person ran by hand, never an error. `magus session --since 2h -o json` is the
form that answers what the fleet has been doing.

Course-correct at explicit checkpoints: after a child proposes new
descendants, when a worker discovers a new API or generated-output dependency,
when ownership drifts, when criteria repeatedly fail, and when status shows
unexpected lock contention or service failure. Pause only the affected branch,
update the ledger and ordering, then resume work that remains independent. Never
guess a PID or signal from stale output; use current status and the host's normal
process controls. A running worker keeps the
constraints it was handed: tightening them means cancel and respawn, not a message
sent mid-flight.

## Integrate and verify

As leases finish:

1. Compare the ledger against the ACTUAL diff since each lease's checkpoint, not
   the paths it reported (`magus graph diff --rev <revision>` for the domain; a
   differing dirty digest means it saw a tree you are not diffing).
2. Reopen each lease's acceptance evidence yourself (`magus query output <ref>`)
   before accepting it; a worker reporting that its criteria passed is not that
   evidence.
3. Resolve cross-lease API changes centrally; never assign the same seam twice.
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
well-isolated leases are usually cheaper than wide fan-out followed by conflict
repair, and the graph is evidence for that judgment rather than permission to
spawn every possible worker.
````

## Short form

The enumeration dropped, the judgment kept - for the most capable readers, not the least; the bar under the heading above shows by how much. This is the always-loaded primary. Both are hand-authored from one source body; see [Skills](../../guides/integrations/agents/skills.md) for the difference.

<details>
<summary>Show the short form</summary>

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

Fan out only after the collision check below REPORTS the leases disjoint; write
sets that look separate is not that check. If the graph supports only one
coherent write set, keep the work local - fanning out one lease adds coordination
and buys nothing. The root agent owns the goal, the budget, the topology,
integration, and final verification, and never hands those out.

## Run the graph-engineering loop

Treat graph engineering as
an acceptance-criteria loop with graph-derived leases: define, partition,
hand out, observe, evaluate, course-correct, integrate. A worker is not complete
until its criteria and assigned validation pass; the root agent decides whether
the top-level goal is complete.

## Set one topology boundary

Before spawning, state one compact budget: maximum simultaneously active agents,
effort tier per lease, whether isolated worktrees are available, and how deep
leases may nest. Editing costs the workspace nothing; what contends is
VALIDATION - the magus runs a lease triggers - so size that cap from the live
pool (`magus status`) rather than a fixed number, and serialize validations
that share a worktree even when their write sets are disjoint. Ask before
exceeding the budget.

Assign validation from the pipeline the workspace composed, not from convention:
`magus describe target ci <project>` names what `ci` chains and in what order,
so a lease gets the narrowest target from that decomposition and the integrator
re-runs the described order, with `magus affected ci` re-proving the whole
composition.

Decide each lease's validation PLANE with its target. A worker environment that
cannot execute magus at all - an isolated tree with no usable binary, or a
guard that routes raw language tools back to targets it cannot run - cannot
validate anything it writes. Mark that lease's validation ROOT-DEFERRED in the
ledger before spawning: the worker writes the tests, stops at the static checks
its environment does run, and says so; the root executes the lease's target
centrally before accepting.

A worker may hand work on again. What it may not do is hand it on without shrinking the
problem. Three rules give it a
definitive end:

- **Every level narrows.** A child's scope is a strict subset of its parent's. A
  worker that would hand on its whole lease should do the work instead.
- **Depth is capped.** Two levels below the root by default: the root hands out leases,
  a lease may hand out parts of its own, and those parts do the work. Say so if you need more.
- **Every lease carries acceptance criteria down with it.** A child inherits its
  parent's criteria plus its own. A lease nobody can evaluate is a lease that cannot
  end, which is what makes depth dangerous rather than the nesting itself.
- **A lease that fails its criteria twice is not re-issued.** The root does it
  locally, or serializes it behind whatever keeps breaking it.
- **Whatever the parent does not hand out, the parent still owns.** A strict subset
  leaks by construction: split "no caller of X remains" into per-project leases and
  the callers in no project belong to nobody, so every lease passes and the goal is
  unmet. Carry a remainder row at each level and close it explicitly.

Pick the model that FITS the lease. That is the whole rule, and it runs both ways:
a mechanical rename does not need the strongest model available, and an ambiguous
API boundary does not get the cheapest one because it looked like less work.

Map work to provider capabilities without assuming model names:

| Tier | Assign |
|---|---|
| principal | architecture, ambiguous ownership, public APIs, migrations, security, integration |
| standard | isolated implementation with a clear contract and bounded project surface |
| economy | mechanical edits, fixtures, docs, inventory, and read-only evidence gathering |

If the host cannot select models or reasoning effort, keep its default. Tool surface
is a separate axis from tier: evidence gathering, scouting, and review get a
read-only tool surface where the host offers one. Never downgrade the root
integration pass or final release gate.

Nesting is allowed when the host supports it, but it does not create a
new budget or a private ownership map. Before a child spawns descendants, it must
report the proposed leases to its parent. The root ledger must then record those
descendants, their parent, effort tier, criteria, and owned paths. Descendants
inherit the ancestor's forbidden paths and may subdivide only the ancestor's
owned paths. Apply worker and cost caps globally, not once per parent.

Keep one integration owner at the root even when the lease tree is deep.
A child coordinates its descendants but may not relax the root's criteria.

## Seed the partition with Magus

Choose the target that will validate the work. `ci` is the release gate, but any
target accepted by `magus affected <target>` can be planned:

```sh
magus affected <target> --plan --max-shards <global-worker-cap>
```

For a proposed change whose paths are known but not edited yet, plan those paths
instead of the current diff:

```sh
printf '%s\n' <repo-relative-path>... | magus affected <target> --stdin --plan --max-shards <global-worker-cap>
```

Read the JSON fields `count`, `max_parallel`, `source`, and `matrix`. A shard is a
history-balanced execution group, not an edit assignment or proof of write
isolation. Use it only as the first partition. If paths are not known, query the
task's symbols, files, and projects before producing a stdin plan.

## Prove that leases do not collide

Classify the union of every lease's proposed paths in one call:

```sh
magus describe file <both leases' paths>... -o json
```

Read the facts: `overlaps` lists each declaration covering more than one
proposed path - a shared write set by construction; `claims[].target` names the
target that regenerates a path (generated outputs have one integration owner,
never hand-edited by workers); `depends_on` carries the owner's direct edges.
Affinity stays with `magus_insight lens=affinity`, and `magus refs <symbol>`
when two leases may touch the same API. A read-only lease has no write set, so it is outside
this analysis entirely.

Two leases may run together only when:

- The combined classification reports no overlaps - source write sets and
  declared outputs disjoint.
- Neither consumes an API or generated artifact the other will change.
- Shared manifests, lockfiles, schemas, workspace configuration, and agent
  instructions have one owner.
- Dependency and temporal-affinity evidence does not indicate that they should
  move together.

Project boundaries alone are insufficient. A declared dependency means group the
work or serialize producer before consumer. Treat strong hidden affinity as a
warning. When evidence is incomplete, reduce parallelism.

## Maintain the global lease ledger

Before spawning, record one row per lease - including the checkpoint it was handed
(`magus vcs checkpoint -o name`: the revision, plus a dirty-patch digest when the
tree is not clean) - and keep descendants in the same table:

| Lease | Parent | Checkpoint | Goal and acceptance criteria | Owned paths | Forbidden paths | Depends on | Tier | Validation | State |
|---|---|---|---|---|---|---|---|---|---|

Every worker prompt must include its row, its LEASE ID, relevant graph
evidence, and the global spawn rule. Require the worker to export
`BAGGAGE=magus.lease=<its id>` before it works - the guard grades its writes only when that is
set. Require it to preserve
unrelated changes, stay inside owned paths, avoid generated outputs, run only its
assigned Magus target, and return changed paths, validation evidence, descendants
it created, and unresolved risks.

The checkpoint you recorded is what you HANDED the lease; the base it
actually LANDED ON is a separate fact, because hosts that isolate workers in
per-worker trees routinely branch them from an older revision than the tree you
partitioned. A worker's first
required act is registering it: run `magus vcs checkpoint -o name` in ITS OWN tree
and call `magus_ledger op=register id=<its row> reported_base=<that token>`. The
answer is a verdict recorded on the row - match, revision-match (same revision,
different uncommitted patch), diverged, or unknown - plus a reading of it that
names both tokens and the next step. It is a FACT and not a gate: every verdict
registers, diverged included. Acting on it is still yours: respawn from the right
revision, or have the worker materialize the files it builds on from the intended
one (`git show <rev>:<path> > <path>`, verifying each blob against
`git rev-parse <rev>:<path>`) and re-put the row's checkpoint.
Also name any fact that will READ as drift to the worker's snapshot - a project
deleted this session, a rename, an index regenerated underneath it - never a
generic "expect drift" line.

Ownership ends when EDITING ends, not when the worker exits. A worker that has
finished writing a contested path announces the release immediately - shrink the
lease's `owned_paths` with another `magus_ledger` put, or message the orchestrator
if the host supports it - and then carries on validating.

That put records each dropped path with the digest it carried at that moment.
Hand the digest to the lease taking the path over: it names the version being
inherited, and one that no longer matches at verification means the waiter built
on a tree the releaser never saw.

Re-put the row on every state change. `op=list` then answers two questions you
would otherwise derive by hand: which live leases claim intersecting
`owned_paths`, and how long since each row was touched. Both are facts, not
verdicts - magus transitions nothing, so a row that has gone quiet is a lease YOU
decide is possibly dead, and a reported overlap is a pair you either intended or
must repartition.

The ledger RECORDS and the agent guard GRADES. A worker that exported
`magus.lease` has each file write judged against these declarations as it
happens: inside its own owned paths passes; inside its forbidden paths, or inside
another live lease's owned paths, is DENIED, and the denial names the owning
lease. A writer magus cannot attribute - a person in their own checkout, or a
worker that never enrolled - is ADVISED and never blocked, and every uncertainty
fails open the same way. It is a seatbelt for harnesses that opt in, not a
sandbox. So a denied worker
COORDINATES and never works around: ask the orchestrator to re-partition, or have
the owning lease release the path with the `owned_paths` put above once it has
finished editing, then retry. Step 1 of Integrate
and verify checks the same boundary against the checkpoint, and that is the half
that does not depend on a worker cooperating.

A read-only lease carries an abbreviated row: no Owned paths, no Forbidden paths. Every
row ends in pass, fail, or NO-RETURN, and the root writes which: silence is not a pass.

Make acceptance criteria observable: named tests,
artifacts, diagnostics, API behavior, or review checks. A child that hands work on must
evaluate its descendants before reporting upward.

Run workers non-blocking by default, and block on one only when your next action
requires its result. An agent spawned merely to wait, poll, or repeat discovery the
root already owns is not an edit lease and spends budget for nothing.

## Observe through the correct control plane

Use the provider's agent or task view to track the lease tree, agent state,
messages, and completion. Use this to keep the root ledger aware of descendants.

Use Magus to watch processes and shared workspace resources:

```sh
magus status --watch=15s
```

This shows Magus process state, lock holders and waiters, and shared-service
state and adoption. It does not show an agent that is thinking without running a
Magus process. Do not replace it with sleep loops, repeated `ps`, or a waiting
agent.

A blocked worker RAISES rather than stalling quietly. Piping the block to `magus
session notify --outcome waiting` (blocked on input) or `--outcome permission` (blocked on
approval) opens a durable request in this repository; no other outcome opens one.
`magus session attention` lists what is open, and `magus session attention -q` prints nothing and exits 1 on an
empty queue, which is the form to test from a loop. Nothing closes a request by
itself: the orchestrator, or any human, disposes it with `magus session dispose
<id> --reason "<why>"`. A worker
that raised one waits for the disposition instead of choosing for itself.

`magus session` is how the root audits what a lease actually RAN, as opposed
to what it reported. Each session carries the lease it was launched under -
the same `magus.lease` channel - along with the spawner label and parent span it
claimed, the targets it finished and how they ended, and the store is keyed by
repository identity, so a worker in its own worktree is still listed here. `magus session --since 2h -o json` is the
form that answers what the fleet has been doing.

Re-plan when nesting, dependencies, ownership, failing
criteria, locks, or services change. Update the ledger before resuming affected
work, and never act on a guessed or stale PID. A running worker keeps the
constraints it was handed: tightening them means cancel and respawn, not a message
sent mid-flight.

## Integrate and verify

As leases finish:

1. Compare the ledger against the ACTUAL diff since each lease's checkpoint, not
   the paths it reported (`magus graph diff --rev <revision>` for the domain; a
   differing dirty digest means it saw a tree you are not diffing).
2. Reopen each lease's acceptance evidence yourself (`magus query output <ref>`)
   before accepting it; a worker reporting that its criteria passed is not that
   evidence.
3. Resolve cross-lease API changes centrally; never assign the same seam twice.
4. Regenerate declared outputs once after source work converges.
5. Re-run `magus affected <target> --plan` over the actual diff. If its shape
   invalidates the original partition, stop parallel integration and reconcile.
6. Read the integrated changeset with `magus diff --impact` before landing it: what
   the fleet's combined edit reaches, who else has been changing it, an estimate of
   the rebuild from recorded run times, what the advisors say, and any note anchored
   to a file it touched. Context, never a verdict, and an empty section means nobody
   could measure it rather than nothing found.
7. Run `magus affected ci` and evaluate the top-level acceptance criteria.

Prefer fewer proven-independent leases over
wide fan-out and conflict repair.
````


</details>
