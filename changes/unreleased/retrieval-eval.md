### Added

- **A report-only retrieval eval for this repository.**
  `magus run retrieval-eval-test .` replays `testdata/retrieval/judgments.jsonl` through
  `query`, `refs` and `explain` and prints precision@10 beside recall@10, MRR, nDCG,
  latency and output bytes per query shape, verb and split; an unjudged file counts as
  not relevant. No score fails it. `MAGUS_RETRIEVAL_EVAL_BASELINE=<file>` writes a
  baseline to compare against.
