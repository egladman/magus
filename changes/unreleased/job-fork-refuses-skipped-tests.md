### Fixed

- **`magus job fork` and `magus job apply` refuse a go test check that skips a test of the
  job's own packages.** A `-run` or `-skip` pattern that leaves out a `Test` function
  beside the job's `.go` write paths is refused. Patterns and `-tags` are read as
  `go test` reads them; `testdata`, `vendor` and nested modules are not packages.
