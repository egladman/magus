### Added

- **swegrade grades Go test logs.** It reads `parse_log_gotest`, the parser every Go row of
  SWE-bench Multilingual names, from `go test -v` result lines as the reference harness does,
  and also from `go test -json` events. Its tests grade logs the Go toolchain prints for a
  fixture package, never pasted text.
