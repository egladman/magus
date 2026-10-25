### Added

- **A Go SDK guide.** `docs/guides/go-sdk.md` opens a workspace, runs a target, asks what
  a change affects, queries the graph, judges a tool call with the guard, and ends with a
  program that is its own harness; every code block is a Go example the tests run. The
  new `(*magus.Magus).Context` lets a program call the graph doors without running a
  target.
