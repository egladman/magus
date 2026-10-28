### Added

- **The `go` spell reports the Go modules in a tree.** Its `discover_projects` claims one
  project per module: the root `go.work`'s `use` list when there is one, else every
  `go.mod` outside `testdata`, `vendor` and `.` or `_` directories. Each project is named
  by its module path, depends on the modules it reaches through a local `replace` (or, in
  a `go.work`, through `require`), wires `test` to `go-test`, `lint` to `go-vet` and
  `build` to `go-build` when the module directory holds a package, and spans every
  directory its `./...` compiles. What it cannot see is reported per module: build tags,
  tests that need a service, `go:generate` drift, a golangci-lint config, cgo sources, and
  a `go.mod` that `./...` or `go.work` leaves out.
