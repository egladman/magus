### Added

- **The `main-logic` and `store-path-export` conformance metrics.** `main-logic` measures a Go main-package function that computes without calling another workspace package; `store-path-export` counts the files outside a state-writing package that reference one of its exported paths. A violation's `home` names the package it belongs in. Neither ships a default.
