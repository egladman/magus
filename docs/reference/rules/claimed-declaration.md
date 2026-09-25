---
title: "claimed-declaration: a leased edit landing in a declaration another live job claims (`run.go#executeStages`)"
description: "A deny rule: it refuses a leased edit landing in a declaration another live job claims (`run.go#executeStages`), and names what to run instead."
tags: [guard, rules, claimed-declaration, deny]
---

# claimed-declaration

A deny rule: it refuses a leased edit landing in a declaration another live job claims (`run.go#executeStages`), and names what to run instead.

## What it catches

A leased edit landing in a declaration another live job claims (`run.go#executeStages`).

## Why

A write path may claim one declaration of a file, so two jobs can start on one file and integrate in order. The claim holds only if an edit into the other job's declaration is caught before it lands, because afterwards both diffs touch it and neither applies over the other. The edit is applied to the file in memory and its changed lines are placed by the same diff-driver matching the job footprint uses, so the declaration this names is the one `magus job wait` would report. It fires only for a job-bound writer whose own claims in the file do not name the declaration, and only when another live job claims a declaration of that file; an edit that lands in the writer's claims, in no one's, or above the first declaration passes. A payload carrying no edit, such as a whole-file write, and a file whose lines cannot be placed stay graded by path alone.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [claimed-declaration]: ...
```

`magus describe rule claimed-declaration` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
