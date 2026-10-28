### Fixed

- **The agent benchmark pairs by task and never prints p = 0.000.** `benchreport analyze`
  compares full and rampant on each task's mean, not rep i against rep i, with an exact
  sign-flip test that says when too few tasks can reach p 0.05 (3 tasks bottom out at 0.25).
  Each comparison carries a paired t interval, d_z and the difference its task count can
  detect; Holm covers pass@1, tokens, dollars and wall clock, and other metrics are
  descriptive. The analysis refuses a comparison when the model, effort, caps, magus version
  or fixture differ across runs, and names the field.
- **pass@k, pass^k and precision/recall in the benchmark report.** pass@1 is the mean of
  per-task rates; pass@k and pass^k are the unbiased estimators per task, averaged at a k
  every task reached. An `answer.json` beside a run gives interrogation tasks tp/fp/fn,
  reported as precision, recall and F1. `--seed` is now unsigned.
