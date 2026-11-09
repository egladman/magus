---
title: "unknown-magus-argv: a magus command or flag the judging binary's command tree does not have"
description: "A deny rule by default: it refuses a magus command or flag the judging binary's command tree does not have, and names what to run instead."
tags: [guard, rules, unknown-magus-argv, deny]
---

# unknown-magus-argv

A deny rule by default: it refuses a magus command or flag the judging binary's command tree does not have, and names what to run instead.

## What it catches

A magus command or flag the judging binary's command tree does not have.

## Why

magus exits 2 on an unknown subcommand or a flag the command does not bind, so the call would not run. The guard reads the same tree as the binary that judges, names the nearest command or flag, and says which build judged. Words past `--` belong to the tool a target runs and are not read.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"unknown-magus-argv": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [unknown-magus-argv]: ...
```

A workspace that sets this rule to `advise` sees `advise [unknown-magus-argv]: ...` instead.

`magus describe rule unknown-magus-argv` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
