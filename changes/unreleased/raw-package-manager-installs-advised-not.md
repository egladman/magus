### Changed

- **Raw package-manager installs are advised, not denied.** `pnpm install`, `npm ci`,
  `uv sync`, `cargo fetch` and `go mod download` point at `magus run install`; naming a
  package leaves the command alone.
