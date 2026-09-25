### Fixed

- **Go ops key their cache on the platform they build for.** The go spell's build, vet,
  test and lint ops fold `GOOS`, `GOARCH`, `GOARM` and `GOAMD64` into their cache keys,
  so a run for another platform no longer replays the host's result.
