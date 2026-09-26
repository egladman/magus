### Fixed

- **The guard lets a tool's help through.** `go clean --help`, `gofmt -h` and
  `magus run --help | grep charm` pass the rules that route work through magus. A help
  flag handed to a program, as in `go run main.go --help`, is still work, and the
  credential and destructive-command rules still refuse.
