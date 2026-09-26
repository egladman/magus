### Added

- **`magus queue gate --go-cache <dir>` builds with Go caches outside the box.** Its
  `go-build` and `pkg/mod` become the gate's `GOCACHE` and `GOMODCACHE`, granted in the
  go spell's modes and nothing else. CI restores the verified toolchain bundle there
  before the gate and saves from it after, on main only. `queue validate` never sets it.
