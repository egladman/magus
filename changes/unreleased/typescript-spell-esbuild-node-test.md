### Added

- **The typescript spell gains `esbuild` and `node-test` ops.** `esbuild` runs the
  project's esbuild with the caller's entry points and flags; the composing target
  declares what it writes. `node-test` runs `node --test` with source maps and
  coverage, printing spec to stdout and writing lcov to the file the caller names
  first (`--test-reporter-destination=<file>`), or `coverage.lcov` when it passes no
  args.
