### Added

- **`fieldwise` reports partial assertions on a result.** With `report-partial-assertions`,
  a testify assertion on one field of a local struct is reported when the test never
  compares that struct whole, and the message asks for one `Equal` on the whole value
  with any time, id or path made deterministic.
