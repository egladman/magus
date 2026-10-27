---
title: "stale-binary: a call that changes state while the magus judging it cannot load this tree's guard policy, or will not after a load-contract change"
description: "A deny rule by default: it refuses a call that changes state while the magus judging it cannot load this tree's guard policy, or will not after a load-contract change, and names what to run instead."
tags: [guard, rules, stale-binary, deny]
---

# stale-binary

A deny rule by default: it refuses a call that changes state while the magus judging it cannot load this tree's guard policy, or will not after a load-contract change, and names what to run instead.

## What it catches

A call that changes state while the magus judging it cannot load this tree's guard policy, or will not after a load-contract change.

## Why

A magus older than the tree, or one answering for a checkout with no ./magus, cannot load the magusfile, so none of the workspace's rules run and a fresh worktree has no record of them to fall back on. The magusfile visibly registering a guard rule is enough to refuse. Edits, spawns, pushes, writing MCP tools and every command that changes state wait until a magus loads the tree. A line passes only when it proves read-only (every program reads, no redirect writes a file, nothing is computed by a substitution), is the fix (`./magus run go-build .`, the relink, the bootstrap over a ./magus that cannot load the tree, each alone on its line), or is a read-only scout recording its work (`magus job exec`, `magus job exit`, `magus buzz --record`). A call that puts back a file the load error names as an unresolved import (`git mv` onto it, `git restore` or `git checkout --` of it, or a write to it) also runs, with an advisory that says so; the bootstrap runs for that failure too. A leased worker is never told to build: there is one binary per base and the main session places it, so the worker's one fix is the placement it is served, `<main checkout>/magus buzz hack/dev/bootstrap-worktree.buzz -- --job <id> --from <main checkout>`. A load failure no newer binary fixes, such as a typo in a checkout with its own ./magus, is policy-unloaded's instead. The load contract is what a binary built before a change needs to load the tree after it: the host declarations, the API locks, the generated boundary bindings, and the Buzz checker and compiler. In a checkout of magus whose ./magus was built at a commit before a load-contract change HEAD carries, every call that changes state is refused until it is rebuilt; reads, VCS recovery and the rebuild run. A ./magus built from uncommitted changes may already carry the change, and the stale-sources notice speaks for it instead. A `git cherry-pick` or `git merge` that brings a load-contract change straight into a checkout of magus whose tree still loads is refused too: it lands through a dry run that applies the whole change in a scratch worktree, compiles ./cmd/magus, checks generator drift and loads the guard policy with the result, because a prefix of a series can leave a tree no binary builds from. While the tree does not load, the pick is the way out, and it runs.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"stale-binary": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [stale-binary]: ...
```

A workspace that sets this rule to `advise` sees `advise [stale-binary]: ...` instead.

`magus describe rule stale-binary` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
