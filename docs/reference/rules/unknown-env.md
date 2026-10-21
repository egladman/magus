---
title: "unknown-env: a retired or misspelled MAGUS_* variable handed to a command"
description: "A deny rule by default: it refuses a retired or misspelled MAGUS_* variable handed to a command, and names what to run instead."
tags: [guard, rules, unknown-env, deny]
---

# unknown-env

A deny rule by default: it refuses a retired or misspelled MAGUS_* variable handed to a command, and names what to run instead.

## What it catches

A retired or misspelled MAGUS_* variable handed to a command.

## Why

A retired or misspelled name is ignored without a word, so the setting the caller meant never takes effect and nothing says so. It fires on the names a command's environment receives: a `NAME=value` prefix, `env NAME=value`, `env -u NAME`, and `export`. It asks config.EnvVarProblem, the check magus's own startup refuses on (MGS1046), so a name the guard denies is one the binary would refuse, and one it cannot prove wrong passes both.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"unknown-env": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [unknown-env]: ...
```

A workspace that sets this rule to `advise` sees `advise [unknown-env]: ...` instead.

`magus describe rule unknown-env` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
