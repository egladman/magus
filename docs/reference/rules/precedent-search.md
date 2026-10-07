---
title: "precedent-search: a hunt for one distinctive name, which refs answers with verified sites"
description: "An advisory by default: it explains, and blocks nothing, on a hunt for one distinctive name, which refs answers with verified sites."
tags: [guard, rules, precedent-search, advise]
---

# precedent-search

An advisory by default: it explains, and blocks nothing, on a hunt for one distinctive name, which refs answers with verified sites.

## What it catches

A hunt for one distinctive name, which refs answers with verified sites.

## Why

A precedent hunt is a search for one distinctive name, and it is the search the graph answers best: refs lists verified sites, so you land on working code instead of assembling it from grep hits. 

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"precedent-search": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [precedent-search]: ...
```

`magus describe rule precedent-search` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
