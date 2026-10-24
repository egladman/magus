---
title: Determinism at chosen points
order: 4
description: magus fixes a step where it can prove the answer and leaves the rest to the model; a person chooses which steps are fixed.
tags: [design, judgment, agents, harness]
---

# Determinism at chosen points

A [harness](../../glossary/harness.md) is the deterministic part of the work on
a change, for a person or an agent
([A harness is not an agent thing](../principles/a-harness-is-not-an-agent-thing.md)).
Around a model's agent loop it is the checks, permission gates, typed
contracts, leases, graph-backed answers and hooks. It comes in degrees. With no harness,
the model judges every step, including the ones that have a right answer: is
this path a generated output, does another job own this file, did the check
pass. A harness that decides every step leaves the model nothing to judge, and
judgment is the reason to run one. magus is built for the hybrid between those
two ends.

magus fixes a step where it can prove the answer and leaves the rest to the
model. The guard allows or denies an action from declared outputs, leases and
VCS state. A job lease fixes the write paths and the check. A spell's typed
contract fixes what it takes and returns. `magus query`, `explain` and `refs`
answer from the graph. Between those points the model reads, plans and writes
the way it chooses. magus supplies facts and verdicts, never the plan, and the
prompt stays the host's and the person's. Skills and MAGUS.md are reference
text an agent loads when a task calls for it. Which steps are fixed is a
[human in the loop](../principles/human-in-the-loop.md) decision: a person writes the guard rule,
forks the job, declares the outputs, and reviews each in version control.

A harness that does more than that starts to own the loop. It rewrites prompts,
sequences tool calls, or tunes its text to one model's habits. Each of those
couples it to one LLM provider. Providers change tool names, context loading
and model behavior on their own schedule, so a harness tuned to one model
overfits to it and has to be tuned again for the next model or the next host.
magus steers away from that on purpose. One guard, one lease store and one
graph serve every host magus wires.

The layers, from the inside out:

1. The model, which proposes the next action.
2. The host's agent loop (Claude Code, Codex, Cursor, OpenCode), which sends
   context to the model and runs the tool calls it returns.
3. The harness magus supplies: guard hooks, skills, MCP tools and job leases,
   attached at the host's hook points.
4. magus's deterministic workspace model: the project graph, each target's
   inputs and outputs, the cache and the job store, which the harness reads.

Layers 2 and 3 are the ones most often merged under one name. magus keeps them
apart: the host owns the loop, and magus owns what the loop can ask and what it
is refused.

## Enforced by

- Verdicts read facts no model produces: declared outputs, leases, VCS state.
- The hook contract is keyed by field rather than host name, which the
  `hostagnostic` and `hostvocab` linters enforce.
- magus builds no model adapter and no agent loop
  ([A record of refusals](../refusals.md)).

## Good

A fact with a right answer comes from the graph, and the model spends its turn
on the part that needs judgment:

```sh
magus describe file docs/MAGUS.md -o json
```

## Bad

```text
system: Before each edit, call tools in this order: plan, search, read, edit, test.
Use at most 3 searches. Prefer the Read tool over Bash.
```

The harness has taken over the plan and tuned it to one host's tool names, so
it has to be rewritten for the next model and the next host.

## See also

- [The outer harness stays outside](the-outer-harness-stays-outside.md)
- [Built for the expensive era](../principles/built-for-the-expensive-era.md)
- [Glossary: harness](../../glossary/harness.md)
