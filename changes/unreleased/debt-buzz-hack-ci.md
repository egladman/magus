### Changed

- **The `hack/ci` scripts return errors as values below their `main`.** `merge-queue`,
  `pull-requests` and `show-failures` return `result\Result::<T>` from every helper and throw
  only in `main`; their `no-throw` rows are gone from `analyzers.yaml`.
