### Added

- **The `image` module assembles OCI images as targets.** `image\assemble` writes one
  Image Manifest from files a target wrote, over scratch or a base layout;
  `assembleIndex` fans platform layouts into an Image Index; `pull` and `push` move
  layouts to and from a registry; `writeSbom`, `writeProvenance` and `attachReferrers`
  attach SPDX and in-toto documents as OCI 1.1 referrers in a new layout; `open` reads a
  layout back. `platform`, `platforms` and `goEnv` turn `"linux/arm64"` into the Platform
  that drives both `go build` and the manifest. Each stage, layer, platform, index and
  push is its own target; [std/examples/image](../../std/examples/image/README.md) shows
  each shape.
- **`fs\mode` parses `"0755"` or `"rwxr-xr-x"`** into the bits `fs\chmod` and image
  placements take; zero is refused.
- **`libs/oci` errors are branchable**: `ErrNotFound`, `ErrUnauthorized`, `ErrOffline`
  and `*StatusError` wrap every registry refusal, and `Client.Resolve` reads a tag.
