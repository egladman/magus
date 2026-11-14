---
title: "step-input: a judged step whose input fails its row's schema, or holds the wrong type where the row points"
description: "A deny rule by default: it refuses a judged step whose input fails its row's schema, or holds the wrong type where the row points, and names what to run instead."
tags: [guard, rules, step-input, deny]
---

# step-input

A deny rule by default: it refuses a judged step whose input fails its row's schema, or holds the wrong type where the row points, and names what to run instead.

## What it catches

A judged step whose input fails its row's schema, or holds the wrong type where the row points.

## Why

A program that judges its agent's steps through magus.Judge declares each kind once, with JSON Pointers to the command and paths the built-in rules read and an optional JSON Schema for the input. A rule that reads a malformed input reads a guess, so the step is refused before any rule runs, and the reason names the schema path or the pointer. The agent can correct its input, so it is a deny rather than an error.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"step-input": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [step-input]: ...
```

A workspace that sets this rule to `advise` sees `advise [step-input]: ...` instead.

`magus describe rule step-input` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
