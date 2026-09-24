### Fixed

- **Concurrent fetches into one repository no longer fail.** Two `git fetch` runs read
  each other's refs mid-update and failed with "bad object"; magus now fetches into a
  repository one at a time.
