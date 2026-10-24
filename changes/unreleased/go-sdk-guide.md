### Added

- **A Go SDK guide.** `docs/guides/go-sdk.md` walks through opening a workspace, listing
  projects and targets, running a target and reading its result and output ref, asking
  what a change affects, querying the graph and judging a tool call with the guard, and
  ends with a program that is its own harness. Every code block is a Go example the test
  suite runs, so the page fails the build before it goes stale.
- **`(*magus.Magus).Context`.** It returns a context carrying the opened workspace, so a
  Go program calls `std.MagusQuery`, `std.MagusRefs` and the other graph doors directly,
  without running a target to get one.
