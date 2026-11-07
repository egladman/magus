### Changed

- **The conventions plugins lint any Go module with magus's `.golangci.yml` unchanged.**
  A plugin reads its `module` from the linted tree's `go.mod` when the setting is absent.
  Settings naming one module's paths sit under `in-module: [{module: <path>, ...}]` and apply only in that module or one nested in it; elsewhere the linter does nothing.
