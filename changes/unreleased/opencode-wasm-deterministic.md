### Fixed

- **Keep generated OpenCode Wasm stable across commits.** Stop stamping the asset with Git revision and dirty-tree metadata so committing unchanged runtime sources does not cause generation drift.
