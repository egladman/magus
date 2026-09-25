### Added

- **The merge queue and the merge driver settle low-risk conflicts.** A conflicted file
  settles when every region both sides changed does, and magus's change classifier
  classes the edit low risk, or `merge_low_risk` opts its code in. Each region is named
  `path#declaration`. git, hg and Sapling run the driver while merging; jj through
  `magus vcs resolve`.
