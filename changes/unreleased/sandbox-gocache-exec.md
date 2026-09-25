### Fixed

- **Sandboxed `go generate` and `go run` work again.** The sandbox grants the Go build cache
  execute as well as read and write: since Go 1.24, `go run` and `go tool` exec the binaries
  they cache there, so every `go run` generator failed with `permission denied`.
