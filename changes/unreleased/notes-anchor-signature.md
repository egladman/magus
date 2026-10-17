### Changed

- **Breaking: a symbol note drifts only when its symbol's signature changes.** A body edit
  or a move now reads clean; file anchors keep their content fingerprint. The
  `anchor-body-changed` code, the `decl_digest` frontmatter key and the console's
  `ANCHOR_STATUS_BODY_CHANGED` status are gone. An older symbol note reads
  `unverifiable-anchor` until re-read and re-recorded with `magus notes edit`.
