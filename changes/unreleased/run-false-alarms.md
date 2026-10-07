### Fixed

- **This repo's `ci` and `coverage-render` format the nested Go modules before reading
  them.** Root lint and test read every module's Go files while a library's `format`
  rewrote them in the same step (MGS4008). A new `libs-format` target runs those formats
  first, reached as a same-project need so `ci`'s own `--` args never reach gofmt.
