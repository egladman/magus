### Added

- **The daemon trims the Go build cache past `go.cache_budget_mb`.** It measures the cache
  every ten minutes and, past the budget, runs `go clean -cache` and files one attention row
  saying it trimmed and the size it saw. A budget of 0 trims nothing, and `magus doctor`
  says so.
