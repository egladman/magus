---
title: "spawn-unvalidated: a spawn for a job whose record was never validated"
description: "A deny rule by default: it refuses a spawn for a job whose record was never validated, and names what to run instead."
tags: [guard, rules, spawn-unvalidated, deny]
---

# spawn-unvalidated

A deny rule by default: it refuses a spawn for a job whose record was never validated, and names what to run instead.

## What it catches

A spawn for a job whose record was never validated.

## Why

A spawn titled `<parent>/<role> <job>` hands that job to its child, and `magus job fork` and `magus job apply` are the doors that validate a record whole and stamp the row. A row with no stamp skipped that check, so its problems would surface in the worker, after the work began. A title naming no live row is spawn-unplaced's, not this rule's. It cannot tell a row an older magus wrote, before rows were stamped, from one a test or a catalog seeded straight into the store: both carry no stamp, and both are re-validated by applying the record again.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"spawn-unvalidated": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [spawn-unvalidated]: ...
```

A workspace that sets this rule to `advise` sees `advise [spawn-unvalidated]: ...` instead.

`magus describe rule spawn-unvalidated` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
