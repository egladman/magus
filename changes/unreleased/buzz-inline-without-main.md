### Changed

- **`magus buzz -e` runs a snippet with no `fun main`.** An inline body takes the
  `--embedded` rules, so imports and statements run as written, the way `python -c`
  does. A body that declares `main` still has it called, and files and stdin stay strict.
