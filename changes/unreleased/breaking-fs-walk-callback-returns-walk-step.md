### Changed

- **Breaking: an `fs\walk` callback returns an `fs\WalkStep`.** It returns
  `WalkStep.next`, `WalkStep.skipDir` or `WalkStep.skipAll`, the way a Go
  `fs.WalkDirFunc` returns nil, `fs.SkipDir` or `fs.SkipAll`. A callback that
  returns a `bool` fails the walk, naming the path it was given.
