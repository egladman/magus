### Changed

- **The `hack/ci` scripts ask for a missing choice when a person runs them on a terminal.**
  `pull-requests.buzz`, `merge-queue.buzz`, `show-failures.buzz`, `run-with-harnesses.buzz`
  and `labels.buzz` offer a list for a run, pull request, issue, repository or subcommand
  left off, and confirm once before an outward step; `--yes` answers it. Workflows pass
  every flag, so their calls behave as before.
