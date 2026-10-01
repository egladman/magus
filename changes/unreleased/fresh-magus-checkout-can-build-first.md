### Fixed

- **A fresh checkout can build its first binary.** With no `magus` in the checkout root,
  the `raw-tool` rule serves `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run
  go-build --no-cache .` for every raw `go` command, and denies it once one exists.
  `go -C <dir> <verb>` and `go <verb> -C <dir>` reach one verdict; a bare `cd <dir>` no
  longer trips the `cd` rule.
