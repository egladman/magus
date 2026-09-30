### Fixed

- **`magus-utils cut` flushes the release manifest before it deletes the fragments.** A
  manifest that fails to land leaves no temp file in `releases/`.
