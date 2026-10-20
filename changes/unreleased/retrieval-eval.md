### Added

- **A report-only retrieval eval for this repository.**
  `magus run retrieval-eval-test .` replays `testdata/retrieval/judgments.jsonl` through
  `query`, `refs` and `explain` and prints recall, MRR, nDCG, latency and output bytes
  per query shape and split. No score fails it. The judgments are committed with the
  tree; `MAGUS_RETRIEVAL_EVAL_BASELINE=<file>` writes a baseline to compare against.
