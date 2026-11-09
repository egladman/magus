### Changed

- **Breaking: the annotated diff and review records move to `types/diff`.**
  `types.DiffHunk` is `diff.Hunk`, `types.DiffReview` is `diff.Review`,
  `types.ReviewOrigin` is `diff.ReviewOrigin`, and the rest drop their `Diff` prefix.
  `types.ReviewVerdict` and the hunk helpers (`Hunks`, `SplitLines`, `MatchDiffDriver`)
  stay in `types`. Buzz names and the JSON shapes are unchanged.
