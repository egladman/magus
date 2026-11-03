---
title: "lease-row-unknown: a leased call judged by a magus older than the job row it reads"
description: "A deny rule by default: it refuses a leased call judged by a magus older than the job row it reads, and names what to run instead."
tags: [guard, rules, lease-row-unknown, deny]
---

# lease-row-unknown

A deny rule by default: it refuses a leased call judged by a magus older than the job row it reads, and names what to run instead.

## What it catches

A leased call judged by a magus older than the job row it reads.

## Why

A row member this build does not declare may widen or narrow what the row permits, and the build cannot tell which, so judging as if the member were absent is a silent default. The refusal names each such member, the binary judging the call and its version, and the remedy: rebuild the ./magus the host hook runs and restart the daemon. Only the root can take that remedy, which ADR 0013 would make a notice; this rule denies anyway because a call the guard cannot judge has no safe default, and passing it would let an old binary approve what a newer row forbids. The spawn gate refuses a spawn bound to such a row, so the root meets the remedy before a worker does. For a call that still arrives, the hook hands a recovery line to the checkout's own ./magus when it was built at or after HEAD or the hook's binary. A row with no checkout, a read-only one, is told to stop and report the risk, since only the root can rebuild.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"lease-row-unknown": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [lease-row-unknown]: ...
```

A workspace that sets this rule to `advise` sees `advise [lease-row-unknown]: ...` instead.

`magus describe rule lease-row-unknown` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
