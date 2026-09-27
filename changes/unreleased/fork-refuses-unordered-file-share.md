### Added

- **`magus job fork` refuses an unordered share of one file across checkouts.** When
  another live job in a different checkout holds the same diffable file and neither job
  orders the other, the fork is refused (MGS3032) naming the holder. A worker may narrow
  a whole-file write path to one declaration of it.
