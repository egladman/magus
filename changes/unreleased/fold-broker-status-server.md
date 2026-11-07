### Changed

- **Breaking: `magus status` is `magus server status`, and `magus broker` is
  `magus server broker`.** Every flag carries over, and `server broker status`,
  `stop` and `units` keep theirs. Neither old name has an alias. Regenerate supervisor units with
  `magus server broker units`: the old ones run `magus broker`.
