---
title: Enforcement, not pinky promises
order: 30
description: A rule written only as prose in an agent's context is a pinky promise; each rule lives at the lowest layer that can enforce it, and skills teach rather than guard.
tags: [design, principle, harness, agents, skills]
---

# Enforcement, not pinky promises

A rule that exists only as a sentence in an agent's context is a pinky promise.
The model may follow it, may forget it after the context compacts, and may
route around it the first time it is in the way. We measured the difference:
advice that only prints gets ignored, and the guard now denies
([ADR 0013](../../decisions/0013-the-guard-denies.md)). The point of a harness
is to move a rule below the prose, to a place where breaking it is not an
option the model gets to weigh.

That needs a clear answer to where each thing lives. Here "tool" means the
broader agent sense, an action a model can call (an MCP tool, a host's built-in
tool), not magus's own Go types.

| layer               | what lives there                                                                                     | in magus                                                                 |
| ------------------- | ---------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| the application     | the facts and the work: what exists, what a change reaches, running a target                          | the graph, the cache, targets, the job store                             |
| the harness         | what must hold whoever acts, checked deterministically before or as the action runs                    | the guard, lint and format targets, the drift gate, `affected ci`        |
| a tool              | one typed action over the application, with its inputs and outputs declared                           | `magus run`, `magus query`, the MCP tools that mirror them               |
| a skill             | how to combine tools for a kind of task, and why; loaded when the task calls for it                   | the installed `magus-*` skills                                           |
| prose in context    | orientation only: where to look, what the words mean                                   | `AGENTS.md`, `MAGUS.md`                                                  |

The rule is to put each thing at the lowest layer that can carry it. A rule
that can be checked belongs in the harness. A fact belongs in the application
and reaches the model through a tool. A skill is not a wrapper around one tool;
a skill that maps one-to-one onto a tool is a tool description in the wrong
place. A skill teaches a workflow across several tools and the judgment between
them, and when it states a rule, the rule is already enforced below it and the
skill only explains it. Whatever is left as prose in context is what nothing
can enforce, and it should be short.

## Enforced by

- The guard denies an action a rule forbids and returns the command to run
  instead; a skill fires when a hook asks for it rather than on the model's
  memory.
- Installed skills are rendered from the binary and stamped, and `magus doctor`
  reports one that drifted.
- An edit to `AGENTS.md` draws the guard's reminder that it loads whole into
  every session, so it holds only what no tool says.

## Good

The rule lives in the harness, and the agent meets it at the moment it
matters:

```text
`sed -i` reads differently on GNU and macOS; use your editor tool.
```

## Bad

```markdown
## IMPORTANT RULES
- NEVER use sed -i. ALWAYS use the editor tool.
- ALWAYS run the tests before committing!!!
```

Capital letters are not enforcement. Each line is a promise the model makes to
itself, and nothing notices when it breaks one.

## See also

- [A harness is not an agent thing](a-harness-is-not-an-agent-thing.md)
- [Determinism at chosen points](../judgment/determinism-at-chosen-points.md)
- [The outer harness stays outside](../judgment/the-outer-harness-stays-outside.md)
