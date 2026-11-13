---
title: "unjudged-step: a step no row declares: a tool call whose input no rule reads, or a step of an undeclared kind"
description: "A deny rule by default: it refuses a step no row declares: a tool call whose input no rule reads, or a step of an undeclared kind, and names what to run instead."
tags: [guard, rules, unjudged-step, deny]
---

# unjudged-step

A deny rule by default: it refuses a step no row declares: a tool call whose input no rule reads, or a step of an undeclared kind, and names what to run instead.

## What it catches

A step no row declares: a tool call whose input no rule reads, or a step of an undeclared kind.

## Why

Every harness wires every tool to the guard, which judges a call by the shape of its input: a command line, a file path read or written, a search, a spawn's prompt, a skill load. A tool whose input has none of those shapes, and which the host's tool rows do not list, could do whatever a rule exists to stop with no rule seeing it; a tool the rows list as touching nothing (a todo list, a plan, a web lookup) passes. The harness spell declares the rows (mgs_listTools) and `magus agent harness install` writes them, one file per capability with one tool name per line; with no rows to read, no tool is known and the refusal names that install. It denies by default: a call no rule can read is a call no rule judged, and the remedy is a tool row. A workspace that accepts unjudged tools sets it to advise. A finished call is never named. A program judging its agent's steps through magus.Judge declares each kind with magus.WithStepRows, and a step no row covers is refused under this rule too.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"unjudged-step": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [unjudged-step]: ...
```

A workspace that sets this rule to `advise` sees `advise [unjudged-step]: ...` instead.

`magus describe rule unjudged-step` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
