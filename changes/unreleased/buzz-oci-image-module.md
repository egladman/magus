### Added

- **Buzz Specs can describe OCI image layouts and multi-platform indexes.**
  The `image` module packages files from built targets into platform images
  and combines them into an image index.

- **Layout carries Created; registry pull stays behind `:update`.**
  `ImageLayout.created` is the config timestamp (RFC 3339), empty when omitted.
  Callers judge age; Layout is not a notes log. Assemble never pulls: a missing
  base fails naming `pullLayout` under the update charm.
