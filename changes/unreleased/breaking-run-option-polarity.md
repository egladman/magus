### Changed

- **Breaking: the run options take positive names.** `WithNoCache()` is now
  `WithCache(bool)` and `WithNoVolatilityRetry()` is now `WithVolatilityRetry(bool)`; both
  default to true, so `WithCache(false)` replaces `WithNoCache()`.
