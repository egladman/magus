### Changed

- **A managed config symlink is bounded by its repository, not its own directory.** A
  `.hg/hgrc`, `.sl/config` or `.gitattributes` that links elsewhere inside the repository
  is written through; one that leaves the repository is still refused. The bound is the
  one the `client` script and the guard read a script path with.
