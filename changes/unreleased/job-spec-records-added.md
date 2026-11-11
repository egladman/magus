### Added

- **`stop-unfiled` refuses a leased subagent's first stop until it files its job result.**
  The refusal names `magus job exit <job> --stdin` and `magus job exit --schema`; a
  read-only row files one too, with an empty `changed_paths`.
- **`stop-unfiled` lets the second stop pass with a note.** Nothing re-prompts the child,
  and `magus job wait` shows the row never passed. Set it to `advise` or `off` through
  `magus\guard.builtins`.
