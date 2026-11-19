### Changed

- **`hack/bench/time-startup.buzz` returns errors as values below its `main`.** Its helpers
  return `result\Result::<T>` and only `main` throws; its `no-throw` row is gone from
  `analyzers.yaml`.
