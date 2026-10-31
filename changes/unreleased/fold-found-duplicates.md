### Changed

- **The proofread decisions table is read by one function, and hg and sl share one bisect.**
  `proofread.RuleDecisions` replaces the two copies in `proofread` and `proofread-train`;
  `magus` bisects Mercurial and Sapling through one `hgBisect`. Behavior and messages are
  unchanged.
- **The Graph Explorer link builder lives in `cmd/magus`.** The one-caller package
  `internal/graph/url` is gone; links are unchanged.
