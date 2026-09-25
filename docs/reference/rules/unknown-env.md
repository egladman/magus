---
title: "unknown-env: a retired or misspelled MAGUS_* variable handed to a command"
description: "A deny rule: it refuses a retired or misspelled MAGUS_* variable handed to a command, and names what to run instead."
tags: [guard, rules, unknown-env, deny]
---

# unknown-env

A deny rule: it refuses a retired or misspelled MAGUS_* variable handed to a command, and names what to run instead.

## What it catches

A retired or misspelled MAGUS_* variable handed to a command.

## Why

A retired or misspelled name is ignored without a word, so the setting the caller meant never takes effect and nothing says so. Measured 2026-09-24: the day MAGUS_NO_WAIT was removed, agents prefixed 462 commands with it, copied from 33 briefs. It fires on the names a command's environment receives: a `NAME=value` prefix, `env NAME=value`, `env -u NAME`, and `export`. It asks config.EnvVarProblem, the check magus's own startup refuses on (MGS1046), so a name the guard denies is one the binary would refuse, and one it cannot prove wrong passes both.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [unknown-env]: ...
```

`magus describe rule unknown-env` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
