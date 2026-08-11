---
name: magus-delegate-ultra
description: "Plan and execute potentially expensive multi-agent work in a magus workspace as an acceptance-criteria loop, using affected shard plans and knowledge-graph evidence to assign collision-resistant edit units, coordinate nested delegation, and choose cost-appropriate effort tiers. Use ONLY when the user names this skill, or asks in their own words for the work to be SPLIT ACROSS AGENTS - \"fan this out\", \"run these in parallel\", \"use several subagents\", \"spin up an agent per package\". Wanting the work faster, sooner, or more thorough is NOT that request: those are asks about the outcome, and this skill is a choice about the method, with a real cost. Never auto-trigger it on ordinary implementation."
license: GPL-3.0-or-later
compatibility: any-agent
metadata:
  source: magus
  agent-skill-version: 30
  knowledge-schema-version: 8
  skill-content: 42f83e0820d1
  skill-variant: simple
---

# Cost-aware graph delegation

This skill is an explicit opt-in to potentially expensive multi-agent work. Use it only when the user
names it or asks for the work to be split across agents. Wanting it faster or more
thorough is not that request.

The root agent owns the goal, global budget, delegation topology, integration,
and final verification. If the graph supports only one coherent edit unit, keep
the work local.

## Run the graph-engineering loop

Treat graph engineering as
an acceptance-criteria loop with graph-derived work units: define, partition,
delegate, observe, evaluate, course-correct, integrate. A worker is not complete
until its criteria and assigned validation pass; the root agent decides whether
the top-level goal is complete.

## Set one global spend and topology boundary

Before spawning, state one compact budget: maximum simultaneously active agents,
effort tier per unit, whether isolated worktrees are available, and whether
nested delegation is allowed. Use the smallest useful fan-out. Unless the user
sets another cap, allow at most three active workers across the entire agent tree.
Ask before exceeding the cap.

Map work to provider capabilities without assuming model names:

| Tier | Assign |
|---|---|
| principal | architecture, ambiguous ownership, public APIs, migrations, security, integration |
| standard | isolated implementation with a clear contract and bounded project surface |
| economy | mechanical edits, fixtures, docs, inventory, and read-only evidence gathering |

If the host cannot select models or reasoning effort, keep its default. Never
downgrade the root integration pass or final release gate.

Nested delegation is allowed when the host supports it, but it does not create a
new budget or a private ownership map. Before a child spawns descendants, it must
report the proposed units to its parent. The root ledger must then record those
descendants, their parent, effort tier, criteria, and owned paths. Descendants
inherit the ancestor's forbidden paths and may subdivide only the ancestor's
owned paths. Apply worker and cost caps globally, not once per parent.

Keep one integration owner at the root even when the delegation tree is deep.
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

## Prove that units do not collide

Inspect the relevant target and projects:

```sh
magus describe target <target> -o json
magus graph deps -o json
magus insight affinity -o json
magus explain project:<path> -o json
```

Use `magus path <a> <b>` for suspicious pairs and `magus refs <symbol>` when two
units may touch the same API. Classify every proposed path with `magus describe
file <path>...`; generated outputs have one integration owner and are never
hand-edited by workers.

Two units may run together only when:

- Their exact source write sets are disjoint.
- Their declared outputs and generated trees do not overlap.
- Neither consumes an API or generated artifact the other will change.
- Shared manifests, lockfiles, schemas, workspace configuration, and agent
  instructions have one owner.
- Dependency and temporal-affinity evidence does not indicate that they should
  move together.

Project boundaries alone are insufficient. A declared dependency means group the
work or serialize producer before consumer. Treat strong hidden affinity as a
warning. When evidence is incomplete, reduce parallelism.

## Maintain the global delegation ledger

Before spawning, record one row per unit and keep descendants in the same table:

| Unit | Parent | Goal and acceptance criteria | Owned paths | Forbidden paths | Depends on | Tier | Validation | State |
|---|---|---|---|---|---|---|---|---|

Every worker prompt must include its row, relevant graph evidence, and the global
spawn rule. Require the worker to preserve unrelated changes, stay inside owned
paths, avoid generated outputs, run only its assigned Magus target, and return
changed paths, validation evidence, descendants it created, and unresolved risks.

Make acceptance criteria observable: named tests,
artifacts, diagnostics, API behavior, or review checks. A delegating child must
evaluate its descendants before reporting upward.

Do not create agents merely to wait, poll, or repeat discovery already owned by
the root. Those jobs spend model budget without creating an independent edit
unit.

## Observe through the correct control plane

Use the provider's agent or task view to track the delegation tree, agent state,
messages, and completion. Use this to keep the root ledger aware of descendants.

Use Magus to watch processes and shared workspace resources:

```sh
magus status --watch=15s
```

This shows Magus process state, lock holders and waiters, and shared-service
state and adoption. It does not show an agent that is thinking without running a
Magus process. Do not replace it with sleep loops, repeated `ps`, or a waiting
agent.

Re-plan when nesting, dependencies, ownership, failing
criteria, locks, or services change. Update the ledger before resuming affected
work, and never act on a guessed or stale PID.

## Integrate and verify

As units finish:

1. Compare reported paths and descendants with the ledger.
2. Verify each unit's acceptance criteria and evidence before accepting it.
3. Resolve cross-unit API changes centrally; never assign the same seam twice.
4. Regenerate declared outputs once after source work converges.
5. Re-run `magus affected <target> --plan` over the actual diff. If its shape
   invalidates the original partition, stop parallel integration and reconcile.
6. Run `magus affected ci` and evaluate the top-level acceptance criteria.

Prefer fewer proven-independent units over
wide fan-out and conflict repair.

<!-- generated by: magus agent install; agent-skill-version: 30; knowledge-schema-version: 8; skill-content: 42f83e0820d1; skill-variant: simple; do not edit, re-run to update -->
