---
title: "merge-side-checkout: a checkout of one merge side over a conflicted file, which discards the merge"
description: "A deny rule: it refuses a checkout of one merge side over a conflicted file, which discards the merge, and names what to run instead."
tags: [guard, rules, merge-side-checkout, deny]
---

# merge-side-checkout

A deny rule: it refuses a checkout of one merge side over a conflicted file, which discards the merge, and names what to run instead.

## What it catches

A checkout of one merge side over a conflicted file, which discards the merge.

## Why

It reads like "undo my edit to this file" and is not: during a merge the working-tree copy IS the merge, and this replaces it wholesale with one side. Measured here: `git checkout MERGE_HEAD -- magusfile.buzz` during a conflict resolution silently dropped the branch's own half of a merged feature. Nothing failed, the gate stayed green, and the feature could not fire until someone read the code days later. For a generated file, `magus vcs resolve` settles every conflicted one by regenerating. To take one side deliberately, say which: `git checkout --ours` or `--theirs`. To keep the merged result, it is already in the file.

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
