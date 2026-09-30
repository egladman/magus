### Changed

- **`magus diff` lists only the symbols a change touched:** `DiffFile.symbols` holds the
  symbols whose definition lines the patch changed, each with a `change` read from the patch
  when no `--baseline` decides it. Package and namespace symbols are never listed, so a
  file's `reach` is no longer every importer of its package.

### Added

- **`DiffSymbol.reachesAPI`:** the callers a changed symbol reaches through calls whose own
  referents sit outside their package or project, with the callers between, from any SCIP
  index that records calls.
- **`magus\diff` takes `opts.patch`,** a unified diff as text, the way `magus diff --patch -`
  reads one.
