---
title: Human-first is the AI integration
order: 7
description: Nothing is designed for an agent first; an interface legible to a person is legible to anything.
tags: [design, principle, agents, cli]
---

# Human-first is the AI integration

magus was built for humans, and agents drive it well anyway, because an
interface legible to a person is legible to anything. That ordering is the
design, and the reason underneath it is structural: models learned to use
tools from decades of humans using tools, so a tool that honors the
conventions humans settled (exit codes, a working directory that means what
it means everywhere, a path as a project's name, plain text) inherits agent
competence for free. A tool that invents its own dialect fails both
audiences, then watches the models learn its workarounds forever. Buzz is the
evidence we have: a language with close to no training data produces better
agent output than the most-trained languages on earth, with explicitness the
only variable in sight. Weigh that as experience rather than benchmark; it is
still the strongest evidence in this section.

This prevents the bolted-on AI integration, papering over a tool people
already struggle with, and the quiet inversion where a person becomes the
secondary user of their own build tool.

The floor under the ordering rule is an absent seam: magus never calls a
model ([Scope](../../scope.md#where-others-drew-it) records the missing adapter as
deliberate), and no CLI flag exists to feed one: every command and its output
are for the person who typed it, and what an agent reads it reads through the
MCP tools. Every review capability is a person's to run with no agent
anywhere in the loop; the agent integration is additive, never load-bearing.
The installed skills are gitignored renderings of the binary, so nothing in
CI can drift-gate them; `magus doctor`'s agent-skills check is what observes
staleness, per workspace ([Scope](../../scope.md#where-the-claim-is-strained)). A
workspace that goes a day, or a Friday, without an agent loses nothing but
speed.

## Enforced by

- An ordering rule with one worked example: the `agents` key in
  `affected ci --plan` is the one agent-specific field in an otherwise
  human-first plan, and the skill-routing hint sits quarantined inside it.
- The one deliberate exception is the job store, an agent-to-agent declaration
  under [Agents propose, humans dispose](../judgment/agents-propose-humans-dispose.md):
  written by agents through MCP and read by people at `magus ls jobs`, so the
  asymmetry is in who ordinarily authors it, not in who may see or write it.
- `magus doctor`'s agent-skills check reports a stale installed skill.

## Good

One flag, one output contract, for whoever is reading:

```sh
magus affected ci --plan -o json
```

## Bad

```sh
magus affected ci --plan --for-agent --llm-summary
```

A flag shaped for a model splits the output into two dialects. The person's
copy and the agent's copy drift, and the agent learns a form no person ever
reads or checks.

## See also

- [Unix survives at the verb, not the binary](unix-survives-at-the-verb.md)
- [The outer harness stays outside](../judgment/the-outer-harness-stays-outside.md)
- [Scope](../../scope.md#where-others-drew-it)
