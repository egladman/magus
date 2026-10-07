---
title: "stage-all: a whole-tree `git add` (-A, -u, ., --all, --update), which sweeps in regenerated output"
description: "An advisory by default: it explains, and blocks nothing, on a whole-tree `git add` (-A, -u, ., --all, --update), which sweeps in regenerated output."
tags: [guard, rules, stage-all, advise]
---

# stage-all

An advisory by default: it explains, and blocks nothing, on a whole-tree `git add` (-A, -u, ., --all, --update), which sweeps in regenerated output.

## What it catches

A whole-tree `git add` (-A, -u, ., --all, --update), which sweeps in regenerated output.

## Why

A magus target writes its declared outputs as it runs, so the tree here is routinely dirty with files you did not edit. `-A` sweeps those and any build residue into a commit about something else, with no signal that it happened, and `-u` reaches the same outputs: it stages every TRACKED change across the whole tree, which is the same sweep minus files that are merely untracked, and a target's declared outputs are ordinarily tracked already. One such call has put a whole regenerated docs site and untouched source files into a commit about something else. `magus vcs add` classifies every dirty path against the declared output globs, keeps a source change and the outputs it produced together, and reports anything undeclared instead of staging it.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"stage-all": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [stage-all]: ...
```

`magus describe rule stage-all` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
