### Fixed

- **`magus job wait` grades the evidence a worker files for its check.** Evidence filed
  under the goal named `check` grades the check instead of failing as undeclared. A go
  test run of the check scoped with `-run` or `-skip` covers the same packages, so it is
  evidence too, and the grade notes the scope.
