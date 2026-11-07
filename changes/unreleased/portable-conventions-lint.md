### Changed

- **The conventions plugins lint any Go module with magus's `.golangci.yml` unchanged.**
  A plugin with a `module` setting now reads the module from the linted tree's `go.mod`
  when the setting is absent, and a tree with no `go.mod` is a plugin error. Settings that
  name paths or packages of one module sit under `in-module: [{module: <module path>, ...}]` and
  apply only when the linted tree is that module or nested in it; a linter whose settings
  are only such a block does nothing elsewhere.
