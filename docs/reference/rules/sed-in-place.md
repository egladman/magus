---
title: "sed-in-place: `sed -i`, whose two spellings destroy each other's work across platforms"
description: "An advisory by default: it explains, and blocks nothing, on `sed -i`, whose two spellings destroy each other's work across platforms."
tags: [guard, rules, sed-in-place, advise]
---

# sed-in-place

An advisory by default: it explains, and blocks nothing, on `sed -i`, whose two spellings destroy each other's work across platforms.

## What it catches

`sed -i`, whose two spellings destroy each other's work across platforms.

## Why

`sed -i` is not portable and the two spellings destroy each other's work. GNU reads `sed -i 's/x/y/' f` as an edit; BSD and macOS read that same script as the BACKUP SUFFIX and take the next argument as the script. `sed -i '' ...` is the macOS spelling and makes GNU edit nothing. So a command that works here mangles the file on the next machine, by WRITING, before anyone reads a diff. An editor tool reads the file first and reports what it changed, and for a whole-tree rename `magus refs <symbol> --occurrences` gives column-precise sites a pattern cannot. Reading with sed is untouched.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"sed-in-place": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [sed-in-place]: ...
```

`magus describe rule sed-in-place` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
