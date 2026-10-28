### Added

- **`magus doctor` measures the Go build cache and the golangci-lint cache.** A cache past
  `go.cache_budget_mb` (default 20480, `MAGUS_GO_CACHE_BUDGET_MB`; 0 turns it off) is
  reported with the command that frees it; for the build cache, that command trims it by
  entry age through `magus/reclaimdisk`. Doctor deletes nothing, and the walk stops at the
  budget or after 500000 entries.
