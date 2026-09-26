---
title: "output-redirect: magus output sent to a file or discarded, which the run log already holds"
description: "A deny rule: it refuses magus output sent to a file or discarded, which the run log already holds, and names what to run instead."
tags: [guard, rules, output-redirect, deny]
---

# output-redirect

A deny rule: it refuses magus output sent to a file or discarded, which the run log already holds, and names what to run instead.

## What it catches

Magus output sent to a file or discarded, which the run log already holds.

## Why

Silencing and keeping are the only two intents and magus has a lever for each: `--silent` says nothing until something fails, and `-o json --tee <file>` keeps the STRUCTURED output rather than console text, which is not a format anything should parse. A target run persists its whole log either way and prints a ref for it, so capturing the console is redundant. It judges where each stream ENDS. Either stream landing in a file fires, and so does stdout landing in /dev/null. `2>/dev/null` fires on `run`, `affected` and `x`, which write their failure block (cause, output ref, reproduce line) to stderr, and passes on every other verb, whose stderr carries at most an error line the exit status also reports. `2>&1` alone passes: both streams still reach the reader. Measured 2026-09-26: 556 of 841 denies were stderr-only.

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
