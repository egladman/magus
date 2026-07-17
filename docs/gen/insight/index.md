---
title: Insight
description: How magus reads VCS history to show where a codebase's attention and risk concentrate - hotspots, temporal coupling, ownership, and trend - as a behavioral complement to static structure.
tags: [insight, vcs, history, hotspots, coupling, ownership, churn, analysis]
---

# Insight

`magus insight <lens>` reads version-control history to show where a codebase's
attention and risk actually concentrate. Static structure tells you how the code is
organized; history tells you how it is _used_ - which files churn, which projects
change together, who owns what. The [man page](manpage/magus-insight.md) lists
the flags; this page is the intent.

## Design intent

- **Behavior over structure.** A dependency graph shows what _could_ affect what.
  History shows what _does_. A file edited every week is a different risk than one
  untouched for a year, even at the same complexity.
- **Contextual by default.** Every lens reflects the working directory's subtree;
  `--workspace` widens it to the whole workspace. Run it where you are asking the
  question.
- **Derived, not stored.** Insight computes from VCS history on demand. There is no
  index to maintain and nothing to keep in sync; the active VCS adapter must report
  per-commit files (git does).

## The lenses

The first argument selects a lens:

- **hotspots** - edit frequency times complexity, the prime refactoring targets.
  The project view heat-colours the dependency graph by churn (with authors,
  recency, blast radius, CI duration); `--files` ranks individual files and renders
  a churn-versus-complexity quadrant.
- **affinity** - projects that change together (temporal coupling). A pair that
  co-changes without either declaring a dependency on the other is a candidate
  architectural smell: a hidden coupling the graph does not know about.
- **ownership** - author concentration: the primary author and their share,
  distinct author count (the bus factor), and abandonment (projects gone quiet).
- **trend** - the recent half of the window against the earlier half. A positive
  delta is a rising hotspot; a negative one is cooling.
- **report** - every lens, plus the knowledge graph's shape from
  [`magus graph stats`](manpage/magus-graph.md), as one whole-workspace
  Markdown document. The magusfile's postflight target writes it to the GitHub
  Actions step summary.

## Bounding the scan

`--commits` caps the scan by count; `--since` bounds it by date (`90d`, `12w`,
`6mo`, `1y`). A wider window is more history and a slower scan, so bound it to the
question: recent hotspots want a short window, an ownership audit a long one.

Each lens accepts `-o text|json|yaml|name`; `hotspots` and `affinity` also render
`-o mermaid` for a diagram you can paste into a review.

## Where it fits

Insight is a read-only lens, never part of a build. Reach for it when you are
deciding _what to work on_ rather than running work: picking a refactor target
(hotspots), questioning an architecture (affinity), or planning ownership
(ownership). The `report` lens in CI turns that into a recurring signal on every
run.

## See also

- [targets.md](targets.md) - the dependency graph insight heat-colours.
- [affected.md](affected.md) - the other VCS-driven command, for building rather than analyzing.
