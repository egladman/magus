### Added

- **`model-tier-context-cap` refuses a leased worker past its tier's context cap.** When a worker's reported context passes the `maxContext` of its row's tier, every write under its lease is refused, and the refusal says to report what it has and stop. Set to advise in `magus\guard.builtins`, it tells the worker once instead.
