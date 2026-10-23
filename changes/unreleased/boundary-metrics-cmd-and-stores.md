### Added

- **The `main-logic` conformance metric.** It measures the body tokens of a Go main-package function that computes rather than wires: not `main` or `init`, no `args []string`, `io.Writer` or workspace-interface parameter, and no call into another workspace package. A violation's `home` is the package its file references most. It ships no default.
- **The `store-path-export` conformance metric.** It counts the non-test files outside a state-writing package that reference one of its exported paths (a string constant or string-returning function named `...Path`, `...Dir`, `...File`, `...FileName` or `...Ext`). A violation's `home` is the store's owner. It ships no default.
