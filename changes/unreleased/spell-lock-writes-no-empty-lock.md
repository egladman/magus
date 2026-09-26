### Changed

- **`magus spell lock --update` writes no lock that pins nothing.** With no remote spell
  declared it removes `magus.lock` instead of writing a header and `version: 1`.
