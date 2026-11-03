### Changed

- **The magus-scout agent files its own row and may grep a literal.** Its last act is
  `magus job exit <row> --stdin`, so the row passes instead of ending no_return. It runs
  `git grep -n -F` once when the graph cannot hold a string. The multi-agent skill says a
  filed result passes a read-only row.
