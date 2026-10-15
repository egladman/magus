### Added

- **`magus diff` reads a forced import alias as a naming smell.** The `import-alias`
  conformance check, on by default as advice, names an aliased import of a package its
  importers rename past the configured value, and a new package that takes an existing
  package's name. Its remedies are renaming the package, or a known entry with the reason.
- **Two conformance metrics measure aliasing.** `alias-share` (anchor 100) is the percent of a
  package's importers that must alias it; `name-clash` (anchor 62) is the percent that also
  import another package of the same name, blamed on the one with fewer importers. Both judge
  packages with `min_subject` importers or more, a new `knowledge.conformance.checks` key that
  defaults to 5. Measured on this tree: 450 of 17782 imports are aliased, 93 packages are
  judged, `alias-share` sits at 30 at p95, and `name-clash` is 62 for `internal/queue/types`,
  its one case.
- **`Graph.ImportAliases`** reports, per workspace package, its importers, aliases, needed
  aliases and same-name clashes.

### Changed

- **Knowledge schema 21.** A file's reference to a Go package carries `import_alias`, and a
  package's namespace node carries `package_name`; graphs built before it rebuild.
