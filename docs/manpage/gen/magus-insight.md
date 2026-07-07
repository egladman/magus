---
title: magus insight
description: "Show where a codebase's attention and risk concentrate: history lenses (hotspots, coupling, ownership, trend) from VCS, and a structure lens (god nodes, orphans, doc coverage) from the knowledge graph."
tags: [cli, magus insight, analysis, hotspots, ownership, coupling, vcs, structure]
---

# magus-insight

Behavioral code analysis from VCS history

## Synopsis

**magus** insight \<lens\> [flags]

## Description

Read version-control history to show where a codebase's attention and
risk concentrate. By default every lens is contextual to the working directory — run
from inside a subtree and it reflects only that subtree's history; pass --workspace to
analyze the whole workspace instead (the fan-in postflight uses this). The active VCS
adapter must report per-commit files (git can).

Lenses (the first argument):

hotspots   Edit frequency × complexity — the prime refactoring targets. The
             project view is the dependency graph heat-coloured by churn (with
             authors, recency, blast radius, and CI duration); --files ranks
             individual files by churn × complexity.
  affinity   Projects that change together (temporal coupling). A hidden pair
             co-changes without either declaring a dependency on the other — a
             candidate architectural smell.
  ownership  Author concentration: the primary author and their share, distinct
             author count (bus factor), and abandonment (projects gone quiet).
  trend      The recent half of the window versus the earlier half: a positive
             delta is a rising hotspot, a negative one is cooling.
  structure  The knowledge-graph lens (no VCS): god nodes (the most connected
             spells, modules, targets - where structural risk concentrates),
             orphans (docs that document nothing, spells no target uses), and doc
             coverage (the share of diagnostics, spells, and modules with a doc).
             --kind scopes every section to one node kind.
  report     Every lens as one whole-workspace Markdown document (the magusfile's
             postflight target writes this to the GitHub Actions step summary).

The history lenses read VCS: --commits caps the scan; --since bounds it by date
(90d, 12w, 6mo, 1y). The structure lens reads the knowledge graph cache-first
instead. Each lens accepts -o text|json|yaml|name; hotspots and affinity also
render -o mermaid (the hotspots file view renders a churn-vs-complexity quadrant).

## Options

**--commits** *int* (default: 500)
: Cap on how many recent commits to scan

**--files**
: hotspots: rank individual files instead of projects

**--kind** *string*
: structure: scope every section to one node kind (spell, target, doc, ...)

**--since** *string*
: Only commits within this window (e.g. 90d, 12w, 6mo, 1y)

**--workspace**
: Analyze the whole workspace instead of the current project/subtree

## Examples

*Prime refactoring targets (files)*

```sh
magus insight hotspots --files
```

*Churn-vs-complexity quadrant*

```sh
magus insight hotspots --files -o mermaid
```

*Hidden architectural coupling*

```sh
magus insight affinity
```

*Bus factor and abandonment*

```sh
magus insight ownership
```

*Rising vs cooling activity*

```sh
magus insight trend --since 90d
```

*Whole-workspace report (all lenses)*

```sh
magus insight report --workspace
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

