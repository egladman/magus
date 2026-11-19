### Fixed

- **An exported accessor returns a copy, or an iterator.** The `Spell` accessors,
  `Chain.Needs`, `StoredOutput.Bytes`, `evidence.Graph.StaleIndexes` and
  `selfupdate.Keyring.Without` no longer hand a caller the slice or map they keep, so a
  caller that sorts or appends edits its own copy. `difftui.Model.Rows` yields its rows,
  beside the new `RowCount` and `RowAt`. The `accessorclone` baseline is empty.
