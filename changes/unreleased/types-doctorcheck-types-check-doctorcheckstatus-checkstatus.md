### Changed

- **`types.DoctorCheck` is `types.Check`, and `DoctorCheckStatus` is `CheckStatus`** (with
  `CheckOK`, `CheckFail` and `CheckAdvice`): a conformance finding is the same record. Go
  names only; JSON keys and proto messages are unchanged. `magus\diff` gains `opts.from`,
  reading a saved review, so `magus diff --impact`'s advisors share one diff.
