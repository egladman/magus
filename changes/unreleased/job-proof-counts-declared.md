### Fixed

- **`job fork` no longer proves a job `alone` against jobs nobody has taken yet.** The write
  proof counts every declared or running job with write paths, bound to this checkout or
  not, so two overlapping forks record `overlapping`. A literal path is matched against a
  glob exactly: `internal/guard/guard.go` no longer overlaps `internal/guard/*_test.go`.
