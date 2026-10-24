### Added

- **`fieldwise` reports partial assertions on a result.** With `report-partial-assertions`,
  a testify `Contains`, `NotContains`, `True`, `False`, `Regexp`, `Len` or `Subset` on a
  field of a local struct, or on a value built from one, and an `Equal` on a lone field,
  are reported when the test never compares that struct whole. The message asks for one
  `Equal` on the whole value, with any time, id or path in it made deterministic.
