### Added

- **An unsupported hook cites the host's docs.** Each `mgs_listUnsupportedHooks` reason
  cites the `https://` URL of the host's docs page, or says `not verified:` with the URL
  checked and the `YYYY-MM-DD` it was read. Any other reason fails the harness's load; a
  vendored schema is not a citation.
