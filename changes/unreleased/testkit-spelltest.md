### Added

- **`spelltest.Register` registers a throwaway spell for one test.** It takes the `spells`
  package's own options, adds a suffix that keeps parallel tests from colliding in the
  process-wide registry, unregisters the spell when the test ends, and returns the name to
  pass to `magus.WithSpell`.
