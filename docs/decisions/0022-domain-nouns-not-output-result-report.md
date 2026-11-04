---
title: "ADR 0022: domain nouns for answer types"
order: 22
description: Answer types in types/ use domain nouns, not Output/Report/Result. Protobuf RPC envelopes use Request/Response only; nested wire messages match the domain nouns. No Insight umbrella type. Domain and protobuf stay separate packages.
tags: [adr, decision, naming, types, go, knowledge, protobuf]
status: accepted
date: 2026-11-01
---

# ADR 0022: domain nouns for answer types

## Context

`types/` held about twenty-five `*Output`, twenty `*Report`, and nine `*Result`
names for structured answers. History metrics mostly used `Output`
(`ConformanceOutput`, `HotspotOutput`); knowledge reads mixed `Report`
(`PrecedentReport`, `SymbolReport`) with `Knowledge*Output`; doctor and describe
used `Report`; action outcomes used `Result` (`ExecResult`, `AffectedResult`).
Nothing in the tree stated which suffix to pick. An `Insight` / `InsightReport`
umbrella named the board that carries those metrics, which was another empty
wrapper. Handlers also mapped to parallel protobuf names (`ConformanceOutput`),
which doubled the vocabulary.

Go does not standardize those suffixes. [Effective Go](https://go.dev/doc/effective_go)
and the package-names blog want a short domain noun, with the package carrying
context (`http.Response`, `go/types.Info`). magus keeps many answer types in one
`types` package, so a bare `Info` is unavailable, but the suffix still should not
be a rigid wrapper word.

Measured on this tree 2026-11-01: `Output` 25, `Report` 20, `Result` 9,
`Record` 7, `View` 2, `Response` 1 (`HTTPResponse`), `Answer` 1
(`KnowledgeAnswer`).

## Decision

1. **A metric or graph answer type is a domain noun.** Name the answer after what
   it is: `Conformance`, `Hotspots`, `Holds`, `Symbols`, `Refs` (or
   `KnowledgeRefs` when `Refs` collides). No `Output`, `Report`, or `Result`
   suffix on those types. No product umbrella (`Insight`, `Lenses`, `Measures`
   as a bundle type): the metrics are the types.
2. **An action outcome prefers a domain noun too** when renamed: `Affected`,
   `Exec` (or keep a short established name). `Result` is not a third vocabulary
   for reads.
3. **Protobuf RPC envelopes use `Request` / `Response` only.** Nested protobuf
   messages use the same domain nouns as `types/` (`Hotspots`, `Conformance`).
   `Response` is not a domain answer type and is not limited to HTTP.
4. **Domain and protobuf stay separate packages.** `types/` never imports
   generated proto; proto is never generated from Go domain structs. Handlers
   map fields at the boundary. Collapsing the two layers is refused.
5. **`Record` stays for one stored or listed entry** (`OutputRecord`,
   `CommitRecord`).
6. `KnowledgeAnswer` stays as the verdict sidecar (`found` / `absent` /
   `unknown`), not a metric payload.
7. **`View` stays for console/API shapes that differ on purpose** (e.g. pointer
   vs value) when they are not a renamed product umbrella. Do not add a new
   `*View` that only means "all the metrics."
8. The board RPC is `HistoryMetricsService.GetHistoryMetrics` with
   `GetHistoryMetricsRequest` / `GetHistoryMetricsResponse` in
   `magus.historymetrics.v1alpha1`. Nested fields are the domain metrics.
9. The majority-shape API replaces precedents with `Hold`, `Holds`, `Count`,
   `Counts`; methods `magus\holds(family)` and `magus\counts(family)`.

| Item                                                                                       | State   |
| ------------------------------------------------------------------------------------------ | ------- |
| This record; refuse new `*Output` / `*Report` answer types in review                       | done    |
| `PrecedentReport` → `Holds`, `SymbolReport` → `Symbols`                                    | done    |
| `ConformanceOutput` → `Conformance`; ceiling ask → `Ceiling`                               | done    |
| History metric `*Output` / `VolatilityReport` → domain nouns                               | done    |
| Delete `Insight` / `InsightReport` / `InsightView` umbrella; no `magus\insight` bundle     | done    |
| Proto `insight` → `historymetrics`; nested nouns; `GetHistoryMetrics*Response`             | done    |
| Lint: RPC I/O ends in Request/Response; nested messages have no `*Output`/`*Report` suffix | done    |
| Action `*Result` renames when those files are touched                                      | planned |
| `magus\graph().precedents` → `magus\holds` / `magus\counts`                                        | planned |

### Principles checked

- [Clear is better than clever](../design/principles/clear-is-better-than-clever.md):
  relied on. A domain noun is the obvious name; a suffix taxonomy is clever.
- [One-vocabulary rule](../glossary/one-vocabulary-rule.md): relied on. One
  answer shape, one name.
- [Break now, hold still later](../design/principles/break-now-hold-still-later.md):
  relied on. Pre-1.0; no compat alias for the old type names.
- [The Go proverbs](../design/principles/the-go-proverbs.md): relied on. Name the
  thing; the package (or the `types` path) supplies context.

## Alternatives

- **Standardize on `Output` for every metric.** Lost: Go does not use that
  suffix; it reads as a framework wrapper. Reopen only if a measurement shows
  agents fail to find domain nouns.
- **Standardize on `Result` (stdlib `BenchmarkResult` habit).** Lost: Result
  means "outcome of running X" in this tree (`ExecResult`); metrics are reads,
  not runs.
- **Generate `.proto` from `types/` (or protobuf tags on domain structs).** Lost:
  the clean separation between application types and the wire. Reopen only with
  evidence the dual map is the defect, not parallel _names_.
- **Keep an `Insight` / `Lenses` / `Measures` domain bundle.** Lost: empty
  umbrella beside the real nouns. The RPC response may still group fields.
- **`GetInsightResponse` as the envelope.** Lost: reintroduces Insight in the
  wire name after the domain drop.
- **Nested packages so types can be `holds.Set` without a suffix.** Lost: answer
  types are shared across CLI, Buzz, and handlers; a package split for naming
  alone is not earned.

## Consequences

- New design docs and APIs say `Holds` / `Conformance`, never `HoldsOutput`.
- Call sites and Buzz mirrors churn on each rename; magus is pre-1.0.
- Protobuf nested messages track domain nouns; RPC envelopes stay Request/Response.
- Console and Buzz reach metrics by name, not through an Insight object.

## Open questions

- Whether `KnowledgeRefsOutput` becomes `Refs` or `KnowledgeRefs` once `refs` and
  the knowledge package are renamed together.
- Whether `Counts` is the lasting noun for ungated peer-group rows, or a later
  measurement prefers `Cohorts`.

## Amendments

### 2026-11-01: reply envelopes and independent metrics

`Response` is the top-level protobuf RPC reply, not HTTP-only. Nested wire
messages use domain nouns. `Insight` / `InsightReport` / `InsightView` and
`magus\insight` as a typed bundle are removed. The console board RPC is
`GetHistoryMetrics` under `magus.historymetrics.v1alpha1`. Domain and protobuf
remain separate packages with a field map at the handler.
