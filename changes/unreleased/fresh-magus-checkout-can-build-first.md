### Fixed

- **A fresh magus checkout can build its first binary.** The `raw-tool` rule advises
  `go run -trimpath ./cmd/magus run go-build --no-cache .` alone in a checkout root
  with no `magus` yet, serves it to every other raw `go` command there (a bare
  `go build -o magus ./cmd/magus` included), and denies it once a binary exists.
  `go -C <dir> <verb>` and `go <verb> -C <dir>` reach one verdict, and a bare
  `cd <dir>` no longer trips the `cd` rule.
