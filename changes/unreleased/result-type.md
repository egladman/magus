### Added

- **A Result type for values that cross a language boundary.** Buzz gets
  `Result::<T>` with `value` and `error: Diagnostic?`. The console and docs get
  `Result<T, E>`, narrowed on `error`, with `MagusError` as the default error and
  `attempt`, `fromPromise` and `parseJSON` helpers. A diagnostic can now carry a
  `fix`.
