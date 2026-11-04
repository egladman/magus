### Added

- **A digest-pinned `image\pull` caches like any input; a tag waits for `:update`.**
  `name@sha256:<hex>` is content-addressed, so a target that pulls it replays offline.
  A tag-only reference is refused outside the update charm. `Layout.created` is the
  config's created time exactly as the config writes it.
