### Removed

- **Breaking for SDK callers: `Limiter`, `NewLimiter`, `DefaultConcurrency` and `WithLimiter` left the root package, and `ReadOutputRecords` moved to `internal/cache` for the CLI.** `Open` builds its own limiter from `magus.yaml`.
