### Added

- **The daemon trims the Go build cache to `go.cache_budget_mb` by entry age.** Every ten
  minutes it removes the entries no build has used longest, whole days at a time, until
  the cache fits, and files one attention row naming what went and what is left. It never
  removes an entry used within the last day, so it never empties the cache. A budget of 0
  trims nothing, and `magus doctor` says so.
