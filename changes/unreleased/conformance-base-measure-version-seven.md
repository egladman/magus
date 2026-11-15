### Fixed

- **The conformance ceiling no longer reads a kept base measure of the old shape as
  zero.** The measure key moved on, so an old entry is measured again. A kept entry
  that decodes with no distributions now fails, naming the file and
  `magus run conformance-base .`.
