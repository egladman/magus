### Added

- **Declared globs take `!` exclusions.** `ctx.writesFiles("gen/*.go", "!gen/runtime.go")`
  declares every generated file but the hand-maintained one, and the same works in
  `ctx.readsFiles`, `ctx.modifiesExistingFiles` and a project's `sources` and `outputs`.
  An exclusion narrows every glob of its own call, in any order, and never another
  call's: the meaning `!` already has in `ctx.glob`. The cache never keys, stores or
  replays an excluded file as an output, `magus clean` never removes it, watch mode
  rebuilds on it, and `magus describe file` no longer calls it an output. A call of
  nothing but exclusions is refused; write a literal leading `!` as `\!`.

### Changed

- **`ctx.glob` refuses a pattern list that is only negations.** `ctx.glob("!site-generate")`
  used to select no targets; it now fails the run, like a declared glob made only of
  exclusions.
