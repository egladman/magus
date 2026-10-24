### Changed

- **Footprint calls take a list.** `ctx.readsFiles(["src/**", "go.mod"])` and
  `ctx.writesFiles(["gen/**"])` replace the variadic string form, which no longer
  loads. Buzz has no variadic parameter, so the list is what lets the calls be typed
  and take a flag.

### Removed

- **`ctx.modifiesExistingFiles`.** Write
  `ctx.writesFiles([...], editsExistingSource: true)`: the target edits files it does
  not own, which must already exist, are part of its source hash, are never replayed on
  a cache hit and are never deleted by `magus clean`. A call to the old name fails and
  names the new one.

### Added

- **A path declared both produced and edited in one target is refused** at load as
  MGS1053, by exact path or by overlapping globs.
- **A target refuses to run when an `editsExistingSource` path or glob matches no
  file**, checked just before it runs so an earlier target can still create it.
