### Added

- **`libs/oci` registry errors can be branched on.** `ErrNotFound`, `ErrUnauthorized`,
  `ErrOffline` and `*StatusError` wrap every registry refusal, and `Client.Resolve` reads
  a tag.
