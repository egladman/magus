### Fixed

- **`libs/embed` and `retrieval-eval-test` key on the Go files they compile.**
  Declaring `testdata/*` replaced the project's sources in the key, so `lint`,
  `build` and `test` replayed a pass after a Go edit.
