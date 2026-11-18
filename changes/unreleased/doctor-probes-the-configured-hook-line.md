### Fixed

- **Doctor probes the hook line a config holds.** The guard-wiring check runs the magus
  invocation each configured guard line carries against a synthetic event, and fails a
  line whose exit is not 0 or 2, since a host runs that call unjudged. A line missing the
  `fail_closed` wrapper the spell renders fails too, naming `magus agent harness install`.
