### Added

- **The `image` module assembles OCI images as targets.** `assemble` and `assembleIndex`
  write a manifest or index; `pull`, `push` and `open` move layouts; `attachReferrers`,
  `writeSbom` and `writeProvenance` add OCI 1.1 referrers; `platform` and `goEnv` drive
  `go build`. `fs\mode` parses `"0755"`, and `libs/oci` errors wrap `ErrNotFound`,
  `ErrUnauthorized` and `ErrOffline`. [Examples](https://github.com/egladman/magus/tree/main/std/examples/image/).
