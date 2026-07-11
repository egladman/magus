---
title: magus insight
description: "Show where a codebase's attention and risk concentrate: hotspots, temporal coupling, ownership, and trend from VCS history, plus volatility from run-outcome history."
tags: [cli, magus insight, analysis, hotspots, ownership, coupling, vcs, volatility, flaky]
---

# magus-insight

Behavioral code analysis from VCS and run-outcome history

## Synopsis

**magus** insight \<lens\> [flags]

## Description

Read history to show where a codebase's attention and risk concentrate.
Four lenses read version-control history; a fifth, volatility, reads run-outcome
history instead. The VCS lenses are contextual to the working directory by default -
run from inside a subtree and each reflects only that subtree's history; pass
--workspace to analyze the whole workspace (the fan-in postflight uses this). The
active VCS adapter must report per-commit files (git can).

VCS-history lenses (the first argument):

hotspots   Edit frequency x complexity - the prime refactoring targets. The
             project view is the dependency graph heat-coloured by churn (with
             authors, recency, blast radius, and CI duration); --files ranks
             individual files by churn x complexity.
  affinity   Projects that change together (temporal coupling). A hidden pair
             co-changes without either declaring a dependency on the other - a
             candidate architectural smell.
  ownership  Author concentration: the primary author and their share, distinct
             author count (bus factor), and abandonment (projects gone quiet).
  trend      The recent half of the window versus the earlier half: a positive
             delta is a rising hotspot, a negative one is cooling.

Run-outcome lens:

volatility Each (project, target) pair's recent pass/fail/volatile record scored
             by its Wilson lower bound; a pair at or above the configured threshold
             is flagged volatile - a flakiness signal, the prime stabilization
             targets. It reads the shared runtime-history file, not git, so it takes
             no --commits/--since window and is always workspace-wide.

report     Every lens plus graph stats as one whole-workspace Markdown document
             (the magusfile's postflight target writes this to the GitHub Actions
             step summary).

The VCS lenses read the commit log: --commits caps the scan; --since bounds it by
date (90d, 12w, 6mo, 1y). Each lens accepts -o text|json|yaml|name; hotspots and
affinity also render -o mermaid (the hotspots file view renders a
churn-vs-complexity quadrant). The structural companion - god nodes, orphans, and
doc coverage from the knowledge graph - is magus graph stats; the report embeds it.

## Options

**--commits** *int* (default: 500)
: Cap on how many recent commits to scan (VCS lenses only)

**--files**
: hotspots: rank individual files instead of projects

**--since** *string*
: Only commits within this window, e.g. 90d, 12w, 6mo, 1y (VCS lenses only)

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

*Flaky (volatile) targets*

```sh
magus insight volatility
```

*Whole-workspace report (all lenses)*

```sh
magus insight report --workspace
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

