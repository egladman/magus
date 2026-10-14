### Added

- **`magus diff` reports a copied call sequence and a sibling family.** Two conformance
  checks are on by default and stay advice. `call-sequence` names a function the change adds,
  renames or re-signs that calls the same workspace functions in the same order as another,
  over a run longer than the configured value; it drops one-statement wrappers, and leaves out
  builders calling mostly one type's methods and Go command verbs taking a `[]string`
  argument list. `sibling-sprawl` names a method or function joining siblings whose names
  differ in one complementary word, such as `RecordCacheHit` and `RecordCacheMiss`, weighed by
  shared parameters, the types carrying the family, and an owner past its `type-methods`
  value. Both are silent without a symbol index.
- **The `call-sequence` conform metric.** The longest ordered run of workspace callees a
  function shares with any other, measured for `source` and `main` and judged like every
  conform metric. Its anchor is 7: a shared run of 8 callees is where the review that
  measured it found its true positives, not a percentile.
