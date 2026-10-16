### Added

- **A report-only retrieval eval for this repository.** `magus run retrieval-eval .`
  replays `testdata/retrieval/judgments.jsonl` through `query`, `refs` and `explain` and
  prints recall, MRR, nDCG, latency and output bytes per query shape and split. No score
  fails it. A person harvests the judgments with `hack/retrieval/harvest.buzz` and
  commits them; `MAGUS_RETRIEVAL_EVAL_BASELINE=<file>` writes a baseline to compare
  against.
