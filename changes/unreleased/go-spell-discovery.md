### Added

- **The `go` spell reports the Go modules in a tree.** Its `discover_projects` claims one
  project per module from `go.work` or each `go.mod`, wires `test`, `lint` and `build` to
  `go-test`, `go-vet` and `go-build`, and draws dependencies from local `replace`
  directives. Build tags, cgo, `go:generate` and modules `./...` leaves out are gaps.
