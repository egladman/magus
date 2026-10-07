### Fixed

- **A project import member reached through `ctx.needs` no longer receives the caller's
  `--` args.** A same-project dependency already dropped them; a cross-project one
  passed them on, so the merge queue's `--inherited=fatal` reached gofmt as a path.
  This repo's `ci` and `coverage-render` now format every nested Go module before root
  lint and test read it (MGS4008).
