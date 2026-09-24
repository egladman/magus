### Fixed

- **A remote-tier miss is visible.** Each prints `<project> not in the remote cache
  (out...)` with the producing run's ref, the end-of-run line counts misses, and `-v`
  adds a digest per key-input class.
