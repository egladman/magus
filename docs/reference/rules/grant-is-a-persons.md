---
title: "grant-is-a-persons: an agent running `magus job grant`, in any spelling and any role"
description: "A deny rule by default: it refuses an agent running `magus job grant`, in any spelling and any role, and names what to run instead."
tags: [guard, rules, grant-is-a-persons, deny]
---

# grant-is-a-persons

A deny rule by default: it refuses an agent running `magus job grant`, in any spelling and any role, and names what to run instead.

## What it catches

An agent running `magus job grant`, in any spelling and any role.

## Why

A grant is a person's one-time approval: `magus job grant <job> <rule> [--for <duration>]` lowers one rule to advice for one job's calls until the duration passes or the job ends, and `--revoke` ends it early. Every call the hook judges is an agent's, so a grant reaching it is not a person's, and it is refused whoever runs it, the root included. The job store also refuses a grant from a process carrying a lease, and no MCP tool exposes one. Only hooks-off is grantable; any other rule is refused, naming the grantable ones. The advice a granted rule gives names who typed the grant, when, and until when.

## Default and override

This rule always takes the decision `deny`. A workspace cannot set it:
naming it in magus\guard.builtins fails to load, because the rule is how a workspace
learns that a rule of its own judged nothing.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [grant-is-a-persons]: ...
```

`magus describe rule grant-is-a-persons` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
