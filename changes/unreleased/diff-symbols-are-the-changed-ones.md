### Changed

- **`magus diff` lists only the symbols a change touched.** `DiffFile.symbols` holds the
  symbols whose definition lines the patch changed, each with a `change` read from the patch
  when no `--baseline` decides it. Package and namespace symbols are never listed, so a
  file's `reach` is no longer every importer of its package.
