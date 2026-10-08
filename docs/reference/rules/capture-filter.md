---
title: "capture-filter: a filter over a run capture or log, which cuts the failure block apart"
description: "An advisory by default: it explains, and blocks nothing, on a filter over a run capture or log, which cuts the failure block apart."
tags: [guard, rules, capture-filter, advise]
---

# capture-filter

An advisory by default: it explains, and blocks nothing, on a filter over a run capture or log, which cuts the failure block apart.

## What it catches

A filter over a run capture or log, which cuts the failure block apart.

## Why

A failure prints five lines together: the target, the cause, an output ref, the command that reads that ref, and the command to reproduce it. A filter keeps the one line it matched and drops the rest, so `grep 'cause:'` keeps the symptom and discards the ref that reads the whole log two lines below it. A range print (`sed -n '1,200p'`) is a filter too: it cuts by POSITION, and the block sits wherever the run left it. The better route is an output contract up front, `-o jsonl --tee <file>`, queried with `jq`. It ADVISES rather than refuses: most such filters are a search the reader needs, and a refused reader then reads the whole file into context. It fires only on a file a filter READS: a host task capture (`tasks/<id>.output`) or a run log (`.magus/logs/<hex>.log`). A pattern shaped like one, such as `grep 'global\.output' cmd/`, is not a capture.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"capture-filter": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [capture-filter]: ...
```

`magus describe rule capture-filter` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
