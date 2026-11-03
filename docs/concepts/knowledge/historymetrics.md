---
title: History metrics
description: How magus reads VCS history to show where a codebase's attention and risk concentrate (hotspots, temporal coupling, ownership, and trend) as a behavioral complement to static structure.
tags: [history-metrics, vcs, history, hotspots, coupling, ownership, churn, analysis]
aliases: [guides/insight, concepts/insight, reference/manpage/magus-insight, concepts/knowledge/insight, concepts/knowledge/historymetrics, guides/historymetrics, concepts/historymetrics]
---

# History metrics

History metrics read version-control history to show where a codebase's attention and risk
concentrate. Static structure tells you how the code is organized; history
tells you how it is _used_: which files churn, which projects change together, who
owns what. This page is the intent.

There is no `magus insight` subcommand and no umbrella `magus\insight()` report.
Each metric is its own Buzz member on a workspace magus has already loaded:

```buzz
import "magus";

final hot = magus\history().hotspots();   // > Hotspots
final aff = magus\history().affinity();   // > Affinity
final own = magus\history().ownership();  // > Ownership
final tr  = magus\history().trend();      // > Trend
```

```text
# over the client tool, for an agent: return magus\history().hotspots()
```

The console board polls `HistoryMetricsService.GetHistoryMetrics` once for every
metric field together; Buzz and agents ask for one metric by name.

## Design intent

- **Behavior over structure.** A dependency graph shows what _could_ affect what.
  History shows what _does_. A file edited every week is a different risk than one
  untouched for a year, even at the same complexity.
- **Contextual by default.** Every metric reflects the directory it is asked about:
  from a magusfile target, the project's own subtree.
- **Derived, not stored.** Metrics compute from VCS history on demand. There is no
  index to maintain and nothing to keep in sync; the active VCS adapter must report
  per-commit files (git does).

## The metrics

Each metric is its own answer type:

- `hotspots`: edit frequency times complexity, the prime refactoring targets.
  Ranks projects by default; ask for `--files` to rank individual files instead.
- `affinity`: projects that change together (temporal coupling). A pair that
  co-changes without either declaring a dependency on the other is a candidate
  architectural smell: a hidden coupling the graph does not know about.
- `ownership`: author concentration: the primary author and their share,
  distinct author count (the bus factor), and abandonment (projects gone quiet).
- `trend`: the recent half of the window against the earlier half. A positive
  delta is a rising hotspot; a negative one is cooling.
- `unreferenced`: code symbols the workspace defines and nothing in it names:
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
- `conformance`: how large and how coupled the code is, measured over the same graph, one
  row per metric and population. Like `unreferenced` it takes no window, and a missing or
  stale symbol index answers `unknown`, never a pass. Read it through the report or
  `magus\change().conformance()`, which also returns each symbol index's freshness.

  A row has a metric: file lines, function lines, methods per type, top-level
  declarations per file, workspace imports per package, tokens in a body whose shape
  another body shares, or exported symbols nothing outside their package uses. It has a
  population too: `source`, `main` (Go's `package main`) or `test`, measured apart so
  moving code into tests cannot shift the source numbers. Generated files and the globs
  in `knowledge.conformance.exclude` belong to none; `excluded` counts what the globs hid, so
  a widened exclusion shows.

  Each row sets two values side by side. `live` is the configured percentile of the code
  today. `value` is the anchor `knowledge.conformance.checks` configures, and a check
  enforces it. They differ on purpose. Nothing enforces a live percentile, because a
  threshold that follows the code loosens as the code grows. `violations` lists what sits
  past `value`.

  `propose` is the lens's only suggestion: the live percentile, offered when at least
  `min_sample` items back it and it sits `step` below `value`. It is never looser than
  `value`. The lens proposes and a person configures: you copy the proposed value into
  `magus.yaml`, and the commit is the review. Nothing here edits the configuration, and a
  policy it cannot honor is an error rather than a quiet fallback.

  Asked for `checks` and a `base` revision, `magus\change().conformance(checks)` also
  returns `ceilings`: each file changed since the base whose count of violations of a
  metric in `checks` rose past its count there, with both counts. This is the
  conformance ceiling. A drop is progress, and a file that holds as many as before
  passes, so violations that predate a change never fail it, and a file absent at the
  base has a ceiling of 0. No count is stored in the workspace. `magus\change(base).mergeBase()`
  exports the base and runs magus's in-process indexers (Go, Buzz, shell) over it with this
  workspace's configuration, as a library call that opens no second workspace and starts no
  run, so a count depends only on the base's bytes, today's policy and the indexer versions.
  The measure is kept in the user cache per commit, policy, indexer versions and magus build, where every
  checkout on the machine reuses it. `magus\change().conformance` only reads it, and a base with none
  is an error naming the step. A language only an external SCIP indexer reads has no base
  measure: each touched file of it is listed in `unmeasured` with its language and why, and
  holds no ceiling. A base source an in-process indexer cannot read is an error, never a pass.

- `grouping`: what splitting work across workers cost in this workspace, read from the
  job outcomes in the history file, not git. Each job is `chained` (one worker whose
  write paths reach two or more projects), `split` (two or more jobs forked under one
  attempt of one parent) or `solo`. Per shape the row sets the context a worker loaded
  before its first write beside the context, wall time, denials and unattributed writes
  per group, folded from the guard's trail when each job ended.

  A shape with fewer than `min_sample` groups, or with most trails rotated away, answers
  `unknown` with its counts and no figure. `unclassified` counts jobs whose projects did
  not resolve. The lens is observational: bigger work is split more often, so it says
  what each shape cost, never which to choose. `magus describe job` prints the same rows
  for the groups that wrote in the job's projects.

## Bounding the scan

`commits` caps the scan by count; `since` bounds it by date (`90d`, `12w`, `6mo`,
`1y`). A wider window is more history and a slower scan, so bound it to the question:
recent hotspots want a short window, an ownership audit a long one. Both are options
on `magus\history().hotspots` / `magus\history().affinity` / `magus\history().ownership` / `magus\history().trend`
(`HistoryMetricsOptions`), and the console polls them together via
`GetHistoryMetrics`.

## Where it fits

History metrics are a read-only lens, never part of a build. Reach for them when you are
deciding _what to work on_ rather than running work: picking a refactor target
(hotspots), questioning an architecture (affinity), or planning ownership
(ownership). Reading them from a magusfile target turns that into a recurring signal:
this repo's own CI summary flags targets whose pass/fail record flaps, straight off
`magus\workspace().volatility()`.

## See also

- [targets.md](../targets.md): the dependency graph hotspots heat-colors.
- [affected.md](../workspace/affected.md): the other VCS-driven command, for building rather than analyzing.
