---
title: Insight
description: How magus reads VCS history to show where a codebase's attention and risk concentrate (hotspots, temporal coupling, ownership, and trend) as a behavioral complement to static structure.
tags: [insight, vcs, history, hotspots, coupling, ownership, churn, analysis]
aliases: [guides/insight, concepts/insight, reference/manpage/magus-insight]
---

# Insight

Insight reads version-control history to show where a codebase's attention and risk
concentrate. Static structure tells you how the code is organized; history
tells you how it is _used_: which files churn, which projects change together, who
owns what. This page is the intent.

There is no `magus insight` subcommand. The lenses are a read of a workspace magus
has already loaded, so they are reached where that workspace already is, two ways,
both computed in the process that already has it open:

```buzz
import "magus";

// From a magusfile target: the typed report, or the rendered document.
final r = magus\insight();               // > InsightReport
magus\log.info(magus\insightMarkdown()); // the INSIGHT.md page
```

```text
# over the client tool, for an agent: return magus\insight().hotspots
```

## Design intent

- **Behavior over structure.** A dependency graph shows what _could_ affect what.
  History shows what _does_. A file edited every week is a different risk than one
  untouched for a year, even at the same complexity.
- **Contextual by default.** Every lens reflects the directory it is asked about:
  from a magusfile target, the project's own subtree.
- **Derived, not stored.** Insight computes from VCS history on demand. There is no
  index to maintain and nothing to keep in sync; the active VCS adapter must report
  per-commit files (git does).

## The lenses

Each lens is a field on the report:

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
  `magus\conformance()`, which also returns each symbol index's freshness.

  A row has a metric: file lines, function lines, methods per type, top-level
  declarations per file, workspace imports per package, tokens in a body whose shape
  another body shares, or exported symbols nothing outside their package uses. It has a
  population too: `source`, `main` (Go's `package main`) or `test`, measured apart so
  moving code into tests cannot shift the source numbers. Generated files and the globs
  in `knowledge.conformance.exclude` belong to none; `excluded` counts what the globs hid, so
  a widened exclusion shows.

  Each row sets two values side by side. `live` is the configured percentile of the code
  today. `value` is what a check enforces, and `source` says where it came from: `ratchet`
  when a person committed it to the record `knowledge.conformance.ratchet` names, `anchor`
  when nothing is committed and the configured bound applies. They differ on purpose.
  Nothing enforces a live percentile, because a threshold that follows the code loosens
  as the code grows. `violations` lists what sits past `value`, apart from the `known`
  entries the record accepts; `stale` lists known entries that no longer violate, which
  a person deletes.

  `propose` is the lens's only suggestion: the live percentile, offered when at least
  `min_sample` items back it and it sits `step` below `value`. It is never looser than
  `value`. The lens proposes and a person commits: you copy the proposed value, its
  sample size and percentile into the record, and the commit is the review. Nothing here
  edits the record or the configuration, and a committed value looser than its anchor,
  or a record that is missing or malformed, is an error rather than a quiet fallback.

  The record is a JSON Lines file, one JSON object per line, so `grep threshold` and
  `diff` read it. Blank lines are skipped, and JSON has no comments: a `threshold`'s
  `why` member carries the reason. The path is workspace-relative, and
  `hack/policy/conformance.jsonl` is the usual name.

  ```text
  {"kind":"schema","schema":1}
  {"kind":"measured","rev":"<rev>","date":"<date>"}
  {"kind":"threshold","metric":"<metric>","population":"<population>","scope":"<scope>","value":<value>,"n":<n>,"percentile":<percentile>,"why":"<why>"}
  {"kind":"known","metric":"<metric>","reason":"<reason>","items":["<item>"]}
  ```

  `schema` is required and names the format version, 1. `scope` and `why` are
  optional, and `scope` is absent when the key has none. A `known` line lists the
  items it accepts in `items`. An unknown kind, member, metric or population, a
  repeated member, a value of the wrong type, a missing required member, a repeated
  line or a key committed twice is an error naming the file and line. The
  tab-separated and single-document JSON formats before this one are refused the same
  way; rewrite them as lines.

## Bounding the scan

`commits` caps the scan by count; `since` bounds it by date (`90d`, `12w`, `6mo`,
`1y`). A wider window is more history and a slower scan, so bound it to the question:
recent hotspots want a short window, an ownership audit a long one. Both are options
on `magus\insight` and `magus\insightMarkdown`, and parameters on the MCP tool.

## Where it fits

Insight is a read-only lens, never part of a build. Reach for it when you are
deciding _what to work on_ rather than running work: picking a refactor target
(hotspots), questioning an architecture (affinity), or planning ownership
(ownership). Reading it from a magusfile target turns that into a recurring signal:
this repo's own CI summary flags targets whose pass/fail record flaps, straight off
the report's volatility lens, and `magus\insightMarkdown()` writes the whole document
to INSIGHT.md or into a CI step summary.

## See also

- [targets.md](../targets.md): the dependency graph insight heat-colors.
- [affected.md](../workspace/affected.md): the other VCS-driven command, for building rather than analyzing.
