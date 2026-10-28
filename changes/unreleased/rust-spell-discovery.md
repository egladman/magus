### Added

- **The rust spell reports the crates a repository holds.** Its `discover_projects`
  claims every `Cargo.toml` with a `[package]` as a project with `cargo-build`,
  `cargo-test` and `cargo-clippy` as its build, test and lint, and turns path
  dependencies, including ones inherited through `workspace = true`, into dependencies on
  the crates they point at. A virtual workspace manifest is claimed with no targets and
  depends on its members; a root package spans its `default-members`. Build scripts,
  features, `[patch]` and `[replace]` path overrides, members that match no crate, crates
  a workspace neither lists nor excludes, path dependencies that leave the workspace, and
  manifests it cannot parse are reported as gaps naming the file.
