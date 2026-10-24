---
title: Built for the largest monorepos
order: 16
description: magus is aimed at the largest monorepos that exist; at that scale performance is the feature, and every hot path is measured.
tags: [design, principle, performance, scale]
---

# Built for the largest monorepos

The problem magus exists for is hard, and I am not pretending otherwise. A
monorepo build tool has to know every project, every input, and what a change
reaches, and answer fast enough that a person or an agent asks it instead of
guessing. At the scale of the largest monorepos anyone runs, that takes
engineering a sane person would not do for a small repository: content-keyed
caches, an index that answers without loading the tree, allocation counted on
hot paths, a garbage collector tuned per phase.

So performance is not a polish pass at the end. A lookup that takes seconds
teaches people and agents to grep instead, and then every guarantee the graph
offers goes unused. The work is warranted, and it is also where clever code is
most tempting, so it runs under the rule in
[Clear is better than clever](clear-is-better-than-clever.md): measure first,
keep the measurement beside the code, and keep the clear version wherever it is
fast enough.

The ambition sets the bar for design, not only for speed. A shape that works
for a hundred projects and falls over at a hundred thousand is not done, and a
feature that needs the whole tree in memory to answer one question is the wrong
shape at any size.

## Enforced by

- The `benchbeside` analyzer: every `// optimization:` comment and every cache
  or memo type sits in a package with a benchmark.
- [Benchmarks](../../benchmarks.md) publishes every run beside the hardware,
  the date and the competing tool versions.
- Nothing yet commits a scale target (projects, files, a lookup's latency at
  that size); that is a debt in [Where this is strained](../strained.md).

## Good

Answer from the index, keyed on content, without loading or rebuilding the
tree:

```sh
magus affected ci --plan -o json
magus refs decodeFile --occurrences
```

## Bad

```text
for each project in the workspace:
    load its magusfile
    hash every input
    compare against the last run
```

It is correct and it is linear in the size of the repository on every
invocation, so the largest repositories, the ones that need the answer most,
wait longest for it.

## See also

- [Clear is better than clever](clear-is-better-than-clever.md)
- [A cache is an accelerator, never a dependency](a-cache-is-an-accelerator.md)
- [Benchmarks](../../benchmarks.md)
