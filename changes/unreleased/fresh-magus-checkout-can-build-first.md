### Fixed

- **A fresh magus checkout can build its first binary.** The `raw-tool` rule advises
  `go build -o magus ./cmd/magus` alone into a checkout root with no `magus` yet, and denies
  it once one exists. `go -C <dir> <verb>` and `go <verb> -C <dir>` reach one verdict, and a
  bare `cd <dir>` no longer trips the `cd` rule.
