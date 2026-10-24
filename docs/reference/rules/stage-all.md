---
title: "stage-all: a whole-tree `git add` (-A, -u, ., --all, --update), which sweeps in regenerated output"
description: "A deny rule by default: it refuses a whole-tree `git add` (-A, -u, ., --all, --update), which sweeps in regenerated output, and names what to run instead."
tags: [guard, rules, stage-all, deny]
---

# stage-all

A deny rule by default: it refuses a whole-tree `git add` (-A, -u, ., --all, --update), which sweeps in regenerated output, and names what to run instead.

## What it catches

A whole-tree `git add` (-A, -u, ., --all, --update), which sweeps in regenerated output.

## Why

A magus target writes its declared outputs as it runs, so the tree here is routinely dirty with files you did not edit. `-A` sweeps those and any build residue into a commit about something else, with no signal that it happened, and `-u` reaches the same outputs: it stages every TRACKED change across the whole tree, which is the same sweep minus files that are merely untracked, and a target's declared outputs are ordinarily tracked already. One such call has put a whole regenerated docs site and untouched source files into a commit about something else. `magus vcs ls-files -o name | git add --pathspec-from-file=-` stages the list magus classified against the declared output globs: dirty source paths no other job holds, with each class it left out named on stderr beside the flag that adds it.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"stage-all": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [stage-all]: ...
```

A workspace that sets this rule to `advise` sees `advise [stage-all]: ...` instead.

`magus describe rule stage-all` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
