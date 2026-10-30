### Added

- **A Result type for values that cross a language boundary.** In Buzz it is
  the generated mirror `Result::<T>`, with a `value` or an `error: Diagnostic?`. The
  console and docs get `Result<T, E>`, a `value` or `error` union narrowed on
  `error`, whose default error is `MagusError`, plus `attempt`, `fromPromise` and
  `parseJSON`. A diagnostic can now carry a `fix`.
