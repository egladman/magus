### Added

- **`describe job` names the narrow test run for a job's write paths.** Each file's
  claiming spell expands it through its test op's `testScope`, and the operands that
  exist in the checkout print under `narrow test scope`, and as `test_scope` in `-o json`
  and `-o template`. A spell that declares no `testScope` says its whole project runs.
