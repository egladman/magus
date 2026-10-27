### Added

- **The `image` module assembles OCI images as targets.** `assemble` and `assembleIndex`
  write a manifest or index; `pull`, `push` and `open` move layouts; `attachReferrers`,
  `writeSbom` and `writeProvenance` add OCI 1.1 referrers; `platform` and `goEnv` turn
  `"linux/arm64"` into the Platform that drives `go build` and the manifest.
  [Examples](https://github.com/egladman/magus/tree/main/std/examples/image/).
