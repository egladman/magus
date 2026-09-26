### Changed

- **A step claims the memory it has been measured to use.** Machine admission claims the
  smaller of a target's `memory_mb` and 1.25 times its highest measured peak over at least
  three successful runs, so a declaration sized for the worst case no longer refuses peers
  that would fit. `magus status` and MGS3009 say whether a figure is declared or measured.
