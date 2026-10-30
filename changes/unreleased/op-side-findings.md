### Changed

- **A command with `sources_each` and no `sources` is refused.** It had no files to run per, so it did nothing.

### Removed

- **The drift notice no longer runs `gofmt -l`, and MGS4009 is gone.** The check hard-coded one language's formatter in shipped Go. `lint` and `format` cover it.
