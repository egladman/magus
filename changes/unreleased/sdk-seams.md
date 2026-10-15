### Changed

- **Breaking for SDK callers: the Go SDK names no internal type.** `go-api-generate` now fails on an exported declaration whose signature, fields or methods name a type from an `internal/` package, so the API lock can no longer pin a call no other module can make.
