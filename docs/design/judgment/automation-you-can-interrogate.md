---
title: Automation you can interrogate
order: 9
description: Each automated verdict ships with a lens that shows its inputs, and each step magus runs is expressible as the tool call it made.
tags: [design, judgment, explain, verification]
---

# Automation you can interrogate

Each automated verdict has a lens that shows its inputs. `affected --explain`
says why a project is in the affected set. `describe target --cache --inputs`
says what hashed into a cache key. `describe file` says whether a file is
generated and by what. `explain` gives a graph node's provenance and edges.
New behavior ships with its lens in the same change: an action magus cannot
account for is a defect in the same class as a wrong answer.

The lens rule has a second half, and it is
[criterion 6 of the standard](../standard.md) pointed at the tool itself: a step
magus runs on your behalf should be expressible as the toolchain invocation it
performed, so a year of running magus leaves you knowing more about `go build`
and your own repository, not less. A spell wraps a tool without hiding it, and
there is no shortcut in there where knowing what your toolchain does stops
being your job. Helpful, but never so helpful that nobody using the tool learns
anything from it.

## Enforced by

- Review: a new verdict lands with its lens in the same change.
- Each run records the tool invocation it made, readable with
  `magus query output <ref>`.

## Good

```sh
magus affected ci --explain
magus describe target test . --cache --inputs
```

## Bad

```text
14 projects up to date (smart cache)
```

It reports a verdict with no inputs behind it. When it is wrong, nothing tells
you why, and nothing teaches you what the cache keyed on.

## See also

- [Optimize the loop that verifies, never the loop that generates](../principles/optimize-the-loop-that-verifies.md)
- [Corrective competence](corrective-competence.md)
