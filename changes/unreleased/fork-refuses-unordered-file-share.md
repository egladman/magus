### Added

- **`magus job fork` refuses an unordered share of one claimable file across checkouts.**
  When a job's write path and another live job's, held in a different checkout, name the
  same single literal file that has a diff driver, and neither job is the other's parent,
  child, or `depends_on` partner, the fork is refused (MGS3032) naming the holder and what
  it has touched in the file so far. A worker may now shrink a whole-file write path to a
  declaration of it, and a child may be handed one declaration of a file its parent owns
  whole.
