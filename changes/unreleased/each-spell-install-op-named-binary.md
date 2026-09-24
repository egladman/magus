### Added

- **Each spell's install op is named binary + capability, one per binary.**
  typescript's `pnpm-install` and `npm-ci`, go's `go-mod-download`, python's
  `uv-sync`, rust's `cargo-fetch`. A project composes the one it needs into its
  own top-level `install` target, which `build`/`test`/`lint` need; `:update`
  rewrites the lockfile as before. An unchanged install replays without forking
  the package manager. macOS seeds `node_modules` from a sibling checkout.
