### Fixed

- **A workspace that is not magus's source migrates its job store again.** Where the
  workspace's history lacks the commit that built magus, no base can judge the binary:
  a clean stamped build or a release migrates and says so, and a modified or unstamped
  build is refused, naming a release or `./magus run go-build .`.
  [State](https://eli.gladman.cc/magus/concepts/state/#store-migrations).
