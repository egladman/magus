---
title: "ADR 0049: retrieval is a static table and a ranker"
order: 49
description: The embedding table reranks BM25F candidates, reaches nobody until they pull it, and cannot recall what lexical search missed. This record keeps the table an OCI artifact that graph build fetches, embeds every node at graph build, ranks with integer weights fit offline, and trains on more languages, with no model call at query time.
tags: [adr, decision, retrieval, embedding, graph]
status: proposed
date: 2026-10-23
---

# ADR 0049: retrieval is a static table and a ranker

## Context

Search over the knowledge graph ranks BM25F candidates and reranks them with a static
embedding table. The numbers below are the research figures; A1, A2, A3 and A6 replace
them with measured rows before this record is accepted.

- The shipped table is potion-code-16M-v2 converted onto a 29k-word vocabulary at 256
  dimensions, int8, 7,897,813 bytes. The conversion loss is A2's number.
- The table only reranks BM25F candidates. The recall it cannot reach: success@100 on
  cosqa-dedup is 77.8 for magustok against 94.0 for the RRF.
- Nobody has the table until they run `graph pull -table` and restart the daemon,
  because `embedtable.Cached` is a `sync.OnceValues` that loads it once per process.
- The stored lexical index is written at build and never read in production.
- Budgets:
  - cold p95 within +15 ms;
  - table at most 8 MiB;
  - graph build within +1.5 s;
  - vectors bit-identical on amd64 and arm64.

## Decision

| Item                                                                                                                                   | State   |
| -------------------------------------------------------------------------------------------------------------------------------------- | ------- |
| The table stays an OCI artifact, digest-pinned in the repo; `graph build` fetches it when the cache lacks that digest (B1)             | planned |
| `graph build` prints one line when it fetches, and offline prints that ranking is lexical only                                         | planned |
| The daemon picks up a newly fetched table without a restart                                                                            | planned |
| Every node is embedded at graph build into an int8 sidecar, and dense top-K joins the candidates (C1)                                  | planned |
| A deterministic code-aware ranker runs, with integer weights fit offline once per release on the judged-dev set (D1, D2)               | planned |
| Training data covers six CoRNStack languages and excludes `hack/**` (E1)                                                               | planned |
| TypeScript training data waits on the corpus study (E2)                                                                                | planned |
| Training is staged; the teacher is GTE-ModernBERT-base under Apache-2.0, or CodeRankEmbed if the F2 gate fails (F1-F3)                 | planned |
| Per-repo adaptation happens at index time and is integer-only: repo-frequency reweighting and graph-neighbour retrofitting (G1, G2)    | planned |
| Each step lands only on a recorded gain in `libs/embed/evals.jsonl` or `testdata/retrieval/baseline.jsonl`                            | planned |

A fetch on a read verb such as `query` stays refused. `graph build` is an explicit verb,
so running it is the consent.

The training license rule: a teacher, dataset or table we ship or train from must be
GPL-3.0-compatible.

### Principles checked

- [No silent defaults](../design/judgment/no-silent-defaults.md): relied on. Offline, `graph build`
  says it ranks lexically only, and a `skipped` list stays loud when a stage does not run.
- [Deterministic answers](0005-deterministic-answers.md): relied on. Runtime is static
  rows and integer arithmetic, with digests pinned per architecture.
- [Running magus is the consent](0014-running-magus-is-the-consent.md): relied on. The fetch
  happens at `graph build`, never at a read verb.
- [A record of refusals](../design/refusals.md): relied on. Four rows record the
  options below: a transformer at query time, late interaction, per-repo fine-tuning
  and a hosted API.
- Departure: none. Eli reversed the 2026-10-16 built-in table on 2026-10-23: data sets ship
  as OCI artifacts, never in the binary.

## Alternatives

### The table built into the binary (+7.9 MB)

Lost: Eli decided data sets are OCI artifacts, and the table adds 7,897,813 bytes to every
binary. Reopen if an install must rank with embeddings with no registry reachable.

### A fetch on a read verb

Lost: it is a network act on a verb such as `query`, which [Running magus is the
consent](0014-running-magus-is-the-consent.md) rules out. Reopen only with new evidence
against that rule.

### Qwen3-Embedding-0.6B as teacher

Lost: it costs about 4x the teacher compute (about 15 h per run on the M5) for MTEB Code 75.42
against GTE-ModernBERT's 71.66, and static distillation from a decoder teacher is unproven.
Reopen if the GTE-ModernBERT student fails the F2 gate.

### LateOn-Code as teacher

Lost as a runtime: ColGrep scored 0.693 NDCG@10 against semble's 0.855 on semble's
benchmark, at 122 ms per query and 5.4 s per index, storing about 300 vectors per unit.
Reopen as a teacher only.

### A transformer at query time

Lost: nomic-embed-text-v1.5 costs 17-18 ms per query and 28-35 ms per document at 547 MB
on the M5, which breaks the 15 ms cold p95 budget and integer determinism. semble's static
model plus a ranker matched CodeRankEmbed (0.824-0.838 against 0.839) at 0.69 ms against
16 ms. Reopen if a model under the budget beats the table on judged-dev.

### Late interaction

Lost: see LateOn-Code above. Reopen on the same evidence.

### Per-repo fine-tuning

Lost: it makes the shipped model a per-repo build artifact (Eli, 2026-10-16). Index-time
integer reweighting and graph retrofitting give repo specificity instead. Reopen if a
gradient-free method fails G2's measurement.

### A hosted API

Lost: magus never calls a model ([Scope](../scope.md)). Nothing reopens it.

## Consequences

- The repository carries a digest per retrained table, not the table; the registry holds
  about 8 MB per version.
- `graph pull -table` folds into `graph build`, and the daemon reloads the table on a new
  digest.
- Every code change regenerates the training sources, and the integrator owns that
  regeneration.
- The judged set is a maintained asset: the holdout is read once per candidate.

## Open questions

- The TypeScript corpus.
- Whether a cross-repo judged set is built for G2's claim.
- Vocabulary shape K2 or K6, pending A3.
