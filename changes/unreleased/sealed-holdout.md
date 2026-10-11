### Added

- **An agent cannot read the sealed proofread sets.** The `sealed-read` guard rule denies a
  shell line that reads, lists, copies or searches `proofread/calibration/sealed` in the XDG
  cache home, however it spells the path. A lint fails a case moved from `fp` to `pass`
  without a `was: fp <round> <id>` note on its header.
