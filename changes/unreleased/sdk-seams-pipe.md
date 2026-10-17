### Removed

- **Breaking for SDK callers: the pipe helpers left the root package for `internal/pipe`, for the CLI.** They are `GatePipe` (now `Gate`), `RecordPipeExit` (now `RecordExit`), `ProcessStdio` (now `Stdio`, handed to a run on its context with `WithStdio`), `WithProcessStdio`, `ReadByMagus`, `ReaderArgv`, `AwaitReaderProof`, `RecordUpstream` and `ReadRecords`.
