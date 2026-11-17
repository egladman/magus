### Changed

- **Only a run you typed installs a project's dependencies.** A run that reads the symbol
  index installs each indexed project first. A read, a background index run and the
  watcher install nothing; their MGS7003 names what is missing and `magus run install
  <project>`. See ADR 0052.
