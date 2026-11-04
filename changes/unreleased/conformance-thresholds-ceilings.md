### Added

- **`magus\change().conformance(checks)` returns the conformance ceilings a change crossed.**
  Each is a file changed since the base whose count of violations of a metric in `checks`
  rose past its count there. The base is measured in process and kept in the user cache
  per commit; no count is stored in the workspace.
