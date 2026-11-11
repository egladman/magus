### Added

- **`stop-unfiled` refuses a leased subagent's first stop until it files its job result.**
  The refusal names `magus job exit <job> --stdin` and `magus job exit --schema`; a
  read-only row files one too, with an empty `changed_paths`.
- **`stop-unfiled` lets the second stop pass with a note.** Nothing re-prompts the child,
  and `magus job wait` shows the row never passed. Set it to `advise` or `off` through
  `magus\guard.builtins`.
- **A job row carries the decisions already made and the evidence behind them.** A fork,
  an apply or a put sets `decisions` (`what`, `why`) and `evidence` (`kind`, `id`,
  `says`); `magus describe job -o json` and `magus\Job` read them, and
  `hack/dev/job-view.buzz` renders them in the spec's view.
