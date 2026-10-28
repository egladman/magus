### Added

- **A spell's cache declares the budget it is trimmed to.** `SandboxCache` gains `trimMb`,
  the size in MiB that reclaiming disk trims the cache back to, least recently modified
  files first and never one modified within the last day. The go spell declares 20480 for
  `GOCACHE`, so the Go build cache budget is the go spell's, not a magus setting, and
  `magus/reclaimdisk`'s `trimCache` trims a directory to such a budget.
