### Changed

- **The conventions plugins take their `module` from the linted tree's `go.mod`.**
  An explicit `module` setting still wins. `stderrprint` treats the module's own packages as callees when `callee-packages` is empty. Each workspace keeps its own `.golangci.yml`.
