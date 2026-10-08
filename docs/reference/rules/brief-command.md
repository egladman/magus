---
title: "brief-command: a spawn or continuation brief that teaches a command the guard denies"
description: "An advisory by default: it explains, and blocks nothing, on a spawn or continuation brief that teaches a command the guard denies."
tags: [guard, rules, brief-command, advise]
---

# brief-command

An advisory by default: it explains, and blocks nothing, on a spawn or continuation brief that teaches a command the guard denies.

## What it catches

A spawn or continuation brief that teaches a command the guard denies.

## Why

A worker runs the commands in its brief as written, so a denied one is refused in every worker the brief reaches, or teaches each of them a way around the refusal. Only what the brief presents as a command is graded, a fenced shell block or an inline code span, with the same rules a shell line gets. A line naming a command to forbid it (never, do not, denied, instead of) is passed over, and a `<placeholder>` reads as a word rather than a redirect.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"brief-command": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [brief-command]: ...
```

`magus describe rule brief-command` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
