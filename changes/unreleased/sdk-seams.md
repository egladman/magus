### Changed

- **Breaking: the Go SDK names no internal type, reads nothing from the process, and lets
  an embedder swap its VCS driver and graph observer.** `go-api-generate` now fails on an
  exported declaration whose signature, fields or methods name a type from an `internal/`
  package, so the API lock can no longer pin a call no other module can make.
  - Removed from the root package, moved to `internal/pipe` for the CLI: `GatePipe`
    (now `Gate`), `RecordPipeExit` (now `RecordExit`), `ProcessStdio` (now `Stdio`, handed
    to a run on its context with `WithStdio`), `WithProcessStdio`, `ReadByMagus`,
    `ReaderArgv`, `AwaitReaderProof`, `RecordUpstream`, `ReadRecords`.
  - Removed: `Server`, `Magus.Serve`, `Magus.SetServer`; the CLI serves `internal/server`
    itself. `Magus.Stream`, `StreamOption`, `WithStreamDryRun`, `WithStreamNull`,
    `WithStreamExtraArgs`, `WithStreamSink` and `StreamAllSentinel` move unexported into
    the CLI, which streams each batch through `Run`, so the guard judges it.
  - Removed: `WithLoadedConfig` and `WithProvider` (the CLI calls
    `internal/workspace.WithLoadedConfig` and `WithTelemetryProvider`); `BuildKnowledgeGraph`,
    `BuildGlobalKnowledgeGraph`, `MergeWorkspaceSymbols`, `MergeWorkspaceSymbolsForRef`,
    `QueryKnowledgeGraph`, `PublishedShards`, `UsePublishedShards`, `RecordedHumanOnly`,
    `RecordedStaleIndexes` and the package-level `SymbolGaps`, `SymbolIndexTimes`,
    `SymbolOccurrences` and `SymbolsOccurrences` (the `*Magus` methods of those names stay;
    the CLI calls `internal/graph/workspacegraph`); `Magus.KnowledgeGraph`,
    `Magus.JobLimits`, `Magus.SpellImports`, `Magus.ServiceSession`, `Magus.Telemetry`,
    `Magus.LastRecordedRun`, `Magus.GetArtifact`, `Magus.ListArtifacts`,
    `Magus.EvaluationReads`, `Magus.ChangeClassifier`, `Magus.ExpandCwd` and
    `types.TargetExpander.ExpandCwd`, `Magus.SetGraphObserver`.
  - Changed: `WorkspaceRegistry` is a struct with `RegisterProject`, `AddProvider` and
    `AddHarness`, no longer an alias carrying the guard-rule setters.
    `Magus.BeginInvocation` takes a `Command`. `Magus.ClassifyEdit` returns the class
    word and why. `WithGraphHistory` takes anything with `PredictDuration`.
    `SymbolOccurrenceRead` is an alias.
  - Changed: `FindRoot("")` is an error; pass the directory to start from.
  - Changed: a workspace opened without `WithEnv` reads no environment, without
    `WithStderr` writes its notices nowhere, and without `WithCommandLine` records an empty
    command line. The CLI passes all three.
  - Added: `WithEnv`, `WithStderr`, `WithCommandLine`; `WithVCSDriver` (and
    `types.VCSOptions.Driver`), which `vcs.Resolve` uses ahead of a name;
    `WithObserver`, a `RunOption` the run's graph builds report to; `SpawnRule`,
    `WithSpawnRule` and `TierCeiling`, the reference rule. A nil spawn rule fails the
    open, and a workspace opened with none refuses every spawn.
