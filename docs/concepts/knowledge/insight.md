---
title: Insight
description: How magus reads VCS history to show where a codebase's attention and risk concentrate - hotspots, temporal coupling, ownership, and trend - as a behavioral complement to static structure.
tags: [insight, vcs, history, hotspots, coupling, ownership, churn, analysis]
aliases: [guides/insight, concepts/insight, reference/manpage/magus-insight]
---

# Insight

Insight reads version-control history to show where a codebase's attention and risk
actually concentrate. Static structure tells you how the code is organized; history
tells you how it is _used_ - which files churn, which projects change together, who
owns what.

There is no `magus insight` subcommand. The lenses are a read of a workspace magus
has already loaded, so they are reached where that workspace already is: from Buzz as
`magus\insight()`, which returns every lens as one typed `InsightReport`, and over MCP
as `magus_insight`. This page is the intent behind them.

## Design intent

- **Behavior over structure.** A dependency graph shows what _could_ affect what.
  History shows what _does_. A file edited every week is a different risk than one
  untouched for a year, even at the same complexity.
- **Contextual by default.** Every lens reflects the directory it is asked about -
  from a magusfile target, the project's own subtree.
- **Derived, not stored.** Insight computes from VCS history on demand. There is no
  index to maintain and nothing to keep in sync; the active VCS adapter must report
  per-commit files (git does).

## The lenses

Each lens is a field on the report:

- **hotspots** - edit frequency times complexity, the prime refactoring targets.
  Ranks projects by default; ask for `--files` to rank individual files instead.
- **affinity** - projects that change together (temporal coupling). A pair that
  co-changes without either declaring a dependency on the other is a candidate
  architectural smell: a hidden coupling the graph does not know about.
- **ownership** - author concentration: the primary author and their share,
  distinct author count (the bus factor), and abandonment (projects gone quiet).
- **trend** - the recent half of the window against the earlier half. A positive
  delta is a rising hotspot; a negative one is cooling.
- **unreferenced** - code symbols the workspace defines and nothing in it names:
  no call from another symbol, and no file outside the one defining them. It reads
  the [knowledge graph](../knowledge.md), not git, so it takes no window and is always
  workspace-wide.

  These are candidates for review, not a delete list. Reflection, interface dispatch,
  build tags, generated call sites, and any consumer outside this workspace are all
  invisible to a static index. The result carries a
  [verdict](../knowledge.md#the-third-verdict-when-an-empty-answer-is-not-a-fact) for
  the same reason: a project whose symbol index was never built contributes no symbols,
  so without it the lens would be most reassuring exactly where it knows least.
  Alongside them the report carries the knowledge graph's shape, the same numbers
  [`magus graph stats`](../../reference/manpage/magus-graph.md) prints.

## Bounding the scan

`--commits` caps the scan by count; `--since` bounds it by date (`90d`, `12w`,
`6mo`, `1y`), passed through the report's argument list. A wider window is more
history and a slower scan, so bound it to the question: recent hotspots want a short
window, an ownership audit a long one.

## Where it fits

Insight is a read-only lens, never part of a build. Reach for it when you are
deciding _what to work on_ rather than running work: picking a refactor target
(hotspots), questioning an architecture (affinity), or planning ownership
(ownership). Reading it from a magusfile target turns that into a recurring signal:
this repo's own CI summary flags targets whose pass/fail record flaps, straight off
the report's volatility lens.

## See also

- [targets.md](../targets.md) - the dependency graph insight heat-colors.
- [affected.md](../affected.md) - the other VCS-driven command, for building rather than analyzing.
