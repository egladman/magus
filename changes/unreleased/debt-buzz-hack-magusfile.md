### Changed

- **The Buzz helpers under `hack/magusfile` return errors as values.** Forty functions
  that declared `!>` now return `result\Result::<T>` and their callers read `.error`; only
  an exported target, or a function a magusfile calls, still throws. The no-throw baseline
  for `hack/magusfile` drops accordingly. Behaviour is unchanged.
