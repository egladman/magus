### Changed

- **The proofread decisions table is read by one function, and hg and sl share one bisect.**
  `proofread.RuleDecisions` replaces the two copies in `proofread` and `proofread-train`;
  `magus` bisects Mercurial and Sapling through one `hgBisect`. Behavior and messages are
  unchanged.
