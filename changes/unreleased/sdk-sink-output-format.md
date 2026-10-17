### Changed

- **Breaking for SDK callers: `NewSink` takes an `output.Format` from `libs/cmdline/output`.** `magus.Format` and its `FormatText`, `FormatJSON`, `FormatYAML`, `FormatJSONL`, `FormatName` and `FormatTemplate` constants are gone; use `output.Text` and its siblings.
