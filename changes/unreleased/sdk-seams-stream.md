### Removed

- **Breaking for SDK callers: `Magus.Stream` and its options move unexported into the CLI.** They are `StreamOption`, `WithStreamDryRun`, `WithStreamNull`, `WithStreamExtraArgs`, `WithStreamSink` and `StreamAllSentinel`; the CLI streams each batch through `Run`, so the guard judges it.
