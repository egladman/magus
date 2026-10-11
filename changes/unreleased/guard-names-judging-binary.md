### Fixed

- **A failed workspace rule says which binary judged.** The note names the binary, its
  version and whether it is this checkout's `./magus`, with the fix. The hook glue says
  once per session when `./magus` exists but is not executable, instead of silently
  judging with the magus on PATH.
