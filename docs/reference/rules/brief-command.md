---
title: "brief-command: a spawn or continuation brief that teaches a command the guard denies"
description: "A deny rule: it refuses a spawn or continuation brief that teaches a command the guard denies, and names what to run instead."
tags: [guard, rules, brief-command, deny]
---

# brief-command

A deny rule: it refuses a spawn or continuation brief that teaches a command the guard denies, and names what to run instead.

## What it catches

A spawn or continuation brief that teaches a command the guard denies.

## Why

A worker runs the commands in its brief as written, so a denied one is refused in every worker the brief reaches, or teaches each of them a way around the refusal. Measured 2026-09-24: 33 briefs seeded 462 prefixes of a retired variable. Only what the brief presents as a command is graded, a fenced shell block or an inline code span, with the same rules a shell line gets. A line naming a command to forbid it (never, do not, denied, instead of) is passed over, and a `<placeholder>` reads as a word rather than a redirect.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [brief-command]: ...
```

`magus describe rule brief-command` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
