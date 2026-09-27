### Removed

- **Breaking: `libs/pricing` moves to `benchmarks/agent/internal/pricing` and drops
  its batch, fast-mode and geo pricing.** Nothing recorded which mode a benchmark
  run used, so `Effective`, `Options`, `Rates.Fast` and `Rates.GeoMultipliers` are
  gone with it; `Rates.Cost` replaces the per-field arithmetic `extract.go` did by
  hand.
