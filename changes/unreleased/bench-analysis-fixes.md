### Fixed

- **The agent benchmark pairs by task and never prints p = 0.000.** `benchreport analyze`
  compares arms on each task's mean with an exact sign-flip test that says when too few
  tasks can reach p 0.05. Holm covers pass@1, tokens, dollars and wall clock, and a
  comparison across differing model, effort, caps, magus version or fixture is refused.
