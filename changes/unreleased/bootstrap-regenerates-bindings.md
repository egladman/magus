### Fixed

- **The checkout bootstrap regenerates stale bindings when the build cannot compile.** It
  runs the `//go:generate` lines of `cmd/magus-utils/boundary_types.go`, builds once more,
  and names the rewritten paths to commit; a second failure prints the compiler output. A
  rewritten path keeps its first character.
