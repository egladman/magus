### Added

- **Declared globs take `!` exclusions.** `ctx.writesFiles("gen/*.go", "!gen/runtime.go")`
  declares every generated file but the hand-maintained one, and the same works in
  `ctx.readsFiles`, `ctx.modifiesExistingFiles` and a project's `sources` and `outputs`.
  An exclusion narrows the globs listed before it, back to the previous exclusion. The
  cache never stores or replays an excluded file, `magus clean` never removes it, and
  `magus describe file` no longer calls it an output. A declaration that opens with an
  exclusion is refused; write a literal leading `!` as `\!`.
