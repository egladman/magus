---
title: Inner harness
order: 4
description: "The host's agent loop: Claude Code, Codex, Cursor or OpenCode. It sends context to the model, runs the tool calls the model proposes and keeps the conversation between turns. magus is never one."
tags: [glossary, agents]
---

# Inner harness

The host's agent loop: Claude Code, Codex, Cursor or OpenCode. It sends context
to the model, parses the tool call the model proposes, runs it and carries the
result into the next turn. It owns the prompt, the tools and what reaches the
model. Most vendors mean this layer when they say harness.

- Is: the loop that executes what the model proposes.
- Is not: the model, the [outer harness](outer-harness.md) around it, or the
  app it runs in.
- magus is never an inner harness: it calls no model and runs no agent loop
  ([The model proposes, the harness executes](../design/principles/the-model-proposes-the-harness-executes.md)).

## Example

`magus describe harness` prints what an inner harness needs wired to reach the
outer one: the guard hooks, the skills and the MCP tools.

```sh
magus describe harness
```

## See also

- [Harness](harness.md)
- [Outer harness](outer-harness.md)
- [Agent](agent.md)
