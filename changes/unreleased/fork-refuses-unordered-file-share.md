### Added

- **`magus job fork` refuses an unordered share of one file across checkouts.** When
  another live job in a different checkout holds the same diffable file and no parent,
  child or `depends_on` chain orders the two, the fork is refused (MGS3032) naming the
  holder. A row nobody has taken is left alone. A worker may narrow a whole-file write
  path to one declaration of it.
