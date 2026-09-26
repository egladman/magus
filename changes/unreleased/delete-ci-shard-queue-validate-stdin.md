### Changed

- **Breaking: `magus queue validate` reads its plan from stdin with `--stdin`; `--plan
  <file>` is gone.** Run `magus queue validate --stdin ... < plan.json`, the same input
  idiom as `affected --stdin` and `run --stdin`. Without `--stdin` validate exits 2.
