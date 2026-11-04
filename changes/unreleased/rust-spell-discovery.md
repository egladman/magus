### Added

- **The rust spell reports the crates a repository holds.** Each `Cargo.toml` with a
  `[package]` is a project with cargo build, test and clippy, and path dependencies,
  inherited ones included, become dependencies. A virtual workspace depends on its
  members. Build scripts, features, patches and unparsable manifests are gaps naming
  the file.
