### Added

- **`magus job fork` refuses an unordered share of one claimable file across checkouts.**
  When two live jobs in different checkouts write the same literal file that has a diff
  driver, and no parent, child or `depends_on` chain orders them, the fork is refused
  (MGS3032) naming the holder and what it has touched. A row nobody has taken is left
  alone.
