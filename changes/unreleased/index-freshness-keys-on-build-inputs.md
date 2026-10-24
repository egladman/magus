### Fixed

- **An index refresh no longer demands a rebuilt ./magus after every merge.** The MGS7003
  that blames a binary built at another commit now names that binary only when a Go build
  input changed between its commit and HEAD.
