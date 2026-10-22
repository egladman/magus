---
title: "sed-in-place: `sed -i`, whose two spellings destroy each other's work across platforms"
description: "A deny rule by default: it refuses `sed -i`, whose two spellings destroy each other's work across platforms, and names what to run instead."
tags: [guard, rules, sed-in-place, deny]
---

# sed-in-place

A deny rule by default: it refuses `sed -i`, whose two spellings destroy each other's work across platforms, and names what to run instead.

## What it catches

`sed -i`, whose two spellings destroy each other's work across platforms.

## Why

`sed -i` is not portable and the two spellings destroy each other's work. GNU reads `sed -i 's/x/y/' f` as an edit; BSD and macOS read that same script as the BACKUP SUFFIX and take the next argument as the script. `sed -i '' ...` is the macOS spelling and makes GNU edit nothing. So a command that works here mangles the file on the next machine, by WRITING, before anyone reads a diff. An editor tool reads the file first and reports what it changed, and for a whole-tree rename `magus refs <symbol> --occurrences` gives column-precise sites a pattern cannot. Reading with sed is untouched.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"sed-in-place": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [sed-in-place]: ...
```

A workspace that sets this rule to `advise` sees `advise [sed-in-place]: ...` instead.

`magus describe rule sed-in-place` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
