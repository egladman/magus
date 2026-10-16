### Added

- **A report-only retrieval eval for this repository.** `magus run retrieval-eval .`
  replays `testdata/retrieval/judgments.jsonl` through `query`, `refs` and `explain` and
  prints recall@5/10/50, MRR, nDCG@10, zero-result and unknown rates, latency and output
  bytes, overall and per query shape and split. No score fails it. A person builds the
  judgments with `magus buzz hack/retrieval/harvest.buzz <holdout-after date>` from local
  Claude Code transcripts, reviews them, and commits them; the file holds query text and
  graph ids only. `MAGUS_RETRIEVAL_EVAL_BASELINE=<file>` writes the report for a person
  to review and commit as `testdata/retrieval/baseline.json`, which later runs compare
  against.
