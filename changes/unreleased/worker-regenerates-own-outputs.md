### Changed

- **A bound worker regenerates the outputs its own change makes stale.** The guard lets
  a worker run the generator of an output its write paths regenerate (MGS3044), so its
  lint no longer fails on drift it could not fix. Any other write, a hand edit, or an
  output another live lease holds is still refused.
