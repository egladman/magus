### Fixed

- **A fork grades a job's check where its holder runs it.** A `--checkout` fork is
  judged in that checkout and may start from a sibling branch checkpoint. A check the
  guard cannot grade refuses the fork as MGS3039, quoting why.
