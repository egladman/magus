### Changed

- **Breaking: magus measures conformance and judges no change.** `types.ConformanceCeilings`,
  `types.ConformanceCeiling`, `types.Ceiling` and `Magus.ConformanceCeilings` are gone.
  `magus\change().conformance()` takes no `checks` and carries no `base`, `ceilings` or
  touched-file `unmeasured`. The comparison lives with the analyzer that reads it.
- **`magus\change().conformanceBase()` returns the measure a change is judged against.** A
  `ConformanceBase` holds the kept merge-base rows and packages, the changed files it was
  measured for, and the changed files it was not. `Magus.ConformanceBase` is the Go form.
