### Fixed

- **Repeated `magus\workspace().unmerged` reads converge.** A row far behind the branch overran its
  one second budget on every read, so it never answered. `RangeCommits` makes one log call
  on every backend, and a row a read cut may spend what is left of the next read.
