---
title: "chained-run: magus runs sequenced with `&&` or `;`, which a pipe of the same stages runs ordered and fail-fast"
description: "A deny rule: it refuses magus runs sequenced with `&&` or `;`, which a pipe of the same stages runs ordered and fail-fast, and names what to run instead."
tags: [guard, rules, chained-run, deny]
---

# chained-run

A deny rule: it refuses magus runs sequenced with `&&` or `;`, which a pipe of the same stages runs ordered and fail-fast, and names what to run instead.

## What it catches

Magus runs sequenced with `&&` or `;`, which a pipe of the same stages runs ordered and fail-fast.

## Why

A pipe of magus runs keeps what the chain was for: stages whose projects overlap run in order, a failed stage stops the ones after it, and the last stage exits with the first failure (MGS3030), so no `set -o pipefail` is needed. It differs from `&&` in one way, which the deny states: a stage on disjoint projects runs alongside the others and finishes even after an upstream stage fails. The pipeline still exits red. The same target chained over several project sets is served the pipe too, which keeps the order the chain chose; the deny names the one call for when order does not matter. Measured 2026-09-27, every such chain was `generate:rw docs` then `generate:rw .`, which one call would reverse. It advises instead, and says why, wherever the pipe would do something else. That covers `||`, a redirect or any other command on the line, and a later `affected` stage, which reads its projects from the diff before the upstream's writes land: `affected generate:rw` stays its own call ahead of `affected ci`. It also covers a later `run` naming no projects, which would inherit the upstream's; a stage that reads stdin, takes no locks, or writes -o output upstream; stages on different binaries or roots; a stage after `go-build` or `build` in magus's own checkout, since every stage starts at once and would run the ./magus being replaced; and Windows, where no pipe proves its upstream.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [chained-run]: ...
```

`magus describe rule chained-run` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
