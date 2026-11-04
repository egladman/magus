### Added

- **A pull request lists the deliberate breaks it carries.** A workflow reads the changed
  files through the API and prints each deviation line the pull request adds, plus any edit
  to the proto gate, `deviations.buzz`, a workflow or a `vcs` setting. It reports and never
  fails. A ledger that is a symlink or not named `*.deviations.jsonl` is an error.
