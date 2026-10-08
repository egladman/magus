### Added

- **The broker reports a crash of any magus process connected to it.** After Go's trace,
  it prints that the defect is magus's, with a link to the code at that build's commit,
  and saves the report under the state directory's `magus/crashes`. It covers every
  goroutine and fatal runtime errors, and nothing recovers the panic.
