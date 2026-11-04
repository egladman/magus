### Changed

- **`go-fmt` now fails on unformatted Go.** The op runs `gofmt -d` over `**/*.go`, which exits 1 on drift, and prints the diff; `format:rw` still rewrites with `-w`. The `go` spell's supported minimum is now Go 1.26, the first release where `gofmt -d` sets its exit status. A repo on an older Go is refused with MGS3005 instead of passing silently.
