---
title: "merge-side-checkout: a checkout of one merge side over a conflicted file, which discards the merge"
description: "A deny rule by default: it refuses a checkout of one merge side over a conflicted file, which discards the merge, and names what to run instead."
tags: [guard, rules, merge-side-checkout, deny]
---

# merge-side-checkout

A deny rule by default: it refuses a checkout of one merge side over a conflicted file, which discards the merge, and names what to run instead.

## What it catches

A checkout of one merge side over a conflicted file, which discards the merge.

## Why

It reads like "undo my edit to this file" and is not: during a merge the working-tree copy IS the merge, and this replaces it wholesale with one side. `git checkout MERGE_HEAD -- magusfile.buzz` during a conflict resolution silently drops the branch's own half of a merged feature: nothing fails, the gate stays green, and the loss surfaces only when someone reads the code. For a generated file, `magus vcs resolve` settles every conflicted one by regenerating. To take one side deliberately, say which: `git checkout --ours` or `--theirs`. To keep the merged result, it is already in the file.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"merge-side-checkout": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [merge-side-checkout]: ...
```

`magus describe rule merge-side-checkout` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
