### Changed

- **`magus job apply -f` takes back what `magus describe job -o json` prints.** The row
  under `job` is applied and the members magus derived beside it are ignored, so describe,
  edit and apply round-trip; a nested row may leave out `schema_version`.
