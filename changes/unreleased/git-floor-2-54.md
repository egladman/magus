### Changed

- **Breaking: magus requires git 2.54 or newer.** Older git fails every git command with
  MGS3005. Before 2.54 a histogram diff could shift its hunks, so `vcs\regions`, job
  footprints and split plans differed from one machine's git to another's. Upgrade git.
