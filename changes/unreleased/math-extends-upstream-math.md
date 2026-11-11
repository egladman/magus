### Changed

- **Breaking: `math\min` and `math\max` over a list are now `math\smallest` and `math\largest`.**
  magus's `math` module now extends Buzz's instead of replacing its declarations, so
  `math\sin`, `math\cos`, `math\pow` and the rest of upstream's members resolve under
  `magus buzz`, and `min`/`max` no longer clash with upstream's two-value
  `minInt`/`minDouble`/`maxInt`/`maxDouble`. Rename list calls: `math\min(xs)` becomes
  `math\smallest(xs)`, `math\max(xs)` becomes `math\largest(xs)`. `magus describe module math`
  lists every member.
