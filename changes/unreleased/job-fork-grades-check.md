### Fixed

- **`magus job fork` and `magus job apply` refuse a go test check that skips a test of the
  job's own packages.** A `-run` or `-skip` pattern that leaves out a `Test` function
  defined beside the job's `.go` write paths is refused, listing the skipped tests and a
  pattern that covers them. The patterns are read as `go test` reads them, from the files
  alone.
- **`magus job fork` and `magus job apply` refuse a check the guard would deny the job's
  worker.** `go-build .` under the `worker-builds-magus` rule is refused at the fork,
  quoting the rule's verdict and naming `go::go-vet`, which compiles without building the
  binary. A workspace whose rules do not load is not graded, and the fork says so.
