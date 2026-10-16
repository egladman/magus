### Changed

- **Breaking: a symbol note drifts only when its symbol's signature changes.** `magus notes
  edit` records the signature the indexer renders. A body edit or a move to another line or
  file now reads clean, and `magus notes verify` reports `drifted-anchor` only on a signature
  change. File anchors keep their content fingerprint. The `anchor-body-changed` code, the
  `decl_digest` frontmatter key and the console's `ANCHOR_STATUS_BODY_CHANGED` status are
  gone; the console now maps `notes verify`'s grade instead of grading anchors itself. A
  symbol note recorded before this change reads `unverifiable-anchor` until someone re-reads
  it and runs `magus notes edit`.
