### Added

- **`model-tier-context-cap` advises a leased worker past its tier's context cap.** When a worker's reported context passes the `maxContext` of its row's tier, its next write is told to report and stop. It advises by default at every level; set to deny in `magus\guard.builtins`, it refuses the worker's writes, as a lease past its deadline is refused.
