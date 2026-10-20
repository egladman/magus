### Fixed

- **The `buf` spell no longer provides `gen/**` as an output of every target.** A
  compile-only target in a buf project, such as `go-build`, captured the generated tree
  and replayed an older capture over freshly generated code on a cache hit. The target
  that runs `buf-generate` declares its outputs with `ctx.writesFiles`.
