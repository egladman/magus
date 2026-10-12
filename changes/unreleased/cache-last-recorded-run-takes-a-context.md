### Changed

- **Breaking for SDK callers: `Magus.LastRecordedRun` takes a `context.Context` first.**
  Like the `Output*` lookups beside it, it now returns the context's error when the
  context is already done.
