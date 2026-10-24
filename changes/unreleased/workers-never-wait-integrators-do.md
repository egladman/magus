### Changed

- **Workers check their own packages and never wait for a seat.** A fork accepts a go test
  check scoped to its write paths' packages. A worker with no seat files its check as
  `owed`, the integration runs it beside the full `go-test` and `lint`, and a scoped run's
  gate shows its scope.
