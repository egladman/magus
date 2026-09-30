### Added

- **Declared globs take `!` exclusions.** `ctx.writesFiles("gen/*.go", "!gen/runtime.go")`
  declares every generated file but the hand-maintained one; `ctx.readsFiles`,
  `ctx.modifiesExistingFiles`, `sources` and `outputs` take them too. An exclusion
  narrows only its own call. The cache, `magus clean` and `magus describe file` never
  treat an excluded file as an output. A call of only exclusions is refused; write a
  literal leading `!` as `\!`.
