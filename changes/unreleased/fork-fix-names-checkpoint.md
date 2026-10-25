### Changed

- **`magus job fork --fix` never moves a checkpoint.** A checkpoint naming a revision the
  repository does not hold stays the MGS3041 error, so a mistyped sha no longer cuts the
  job's checkout from the forker's HEAD without a word under `-s`.
