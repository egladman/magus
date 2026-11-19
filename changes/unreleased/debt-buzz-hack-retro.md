### Changed

- **The `hack/retro` scripts return errors as values.** `trace.buzz`, `denials.buzz`, `rule-precision.buzz`
  and `tests.buzz` return a `Result` below their `main`, which alone throws; the no-throw
  baseline rows for `hack/retro` fall.
