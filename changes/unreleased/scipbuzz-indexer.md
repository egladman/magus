### Added

- **`scip-buzz`, a SCIP indexer for Buzz.** The new `libs/scipbuzz` module reads a
  project's `.buzz` files without running them and indexes top-level declarations,
  references through every import form, host-module members and locals, with exact
  UTF-8 ranges. magus does not use it yet.
