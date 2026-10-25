---
title: "waiver-is-a-persons: an agent running `magus gate waive`, in any spelling and any role"
description: "A deny rule by default: it refuses an agent running `magus gate waive`, in any spelling and any role, and names what to run instead."
tags: [guard, rules, waiver-is-a-persons, deny]
---

# waiver-is-a-persons

A deny rule by default: it refuses an agent running `magus gate waive`, in any spelling and any role, and names what to run instead.

## What it catches

An agent running `magus gate waive`, in any spelling and any role.

## Why

A waiver is a person's say-so that one commit may go out with no passing local gate: `magus gate waive <commit> --reason "..."` records who typed it, when, the commit and the reason, and push-ungated then lets a push or `gh pr create` of that commit through, naming the waiver. `--revoke` ends it. Every call the hook judges is an agent's, so a waiver reaching it is not a person's, and it is refused whoever runs it, the root included. The waivers store also refuses a waiver from a process carrying a lease.

## Default and override

This rule always takes the decision `deny`. A workspace cannot set it:
naming it in magus\guard.builtins fails to load, because the rule is how a workspace
learns that a rule of its own judged nothing.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [waiver-is-a-persons]: ...
```

`magus describe rule waiver-is-a-persons` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
