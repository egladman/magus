---
title: "output-redirect: magus output redirected to a file, which the run log already holds"
description: "A deny rule: it refuses magus output redirected to a file, which the run log already holds, and names what to run instead."
tags: [guard, rules, output-redirect, deny]
---

# output-redirect

A deny rule: it refuses magus output redirected to a file, which the run log already holds, and names what to run instead.

## What it catches

Magus output redirected to a file, which the run log already holds.

## Why

There is no legitimate shape of this against magus. Silencing and keeping are the only two intents and magus has a lever for each: `--silent` says nothing until something fails, and `-o json --tee <file>` keeps the STRUCTURED output rather than console text, which is not a format anything should parse. A target run persists its whole log either way and prints a ref for it, so capturing the console is redundant.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [output-redirect]: ...
```

`magus describe rule output-redirect` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
