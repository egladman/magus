### Changed

- **A measured slot claim grows when the claim held the run back.** A run that kept 90% of
  its slots busy is saturated, and the next claim doubles, up to the budget, until a run
  granted more slots comes back unsaturated. Only unsaturated runs feed the 75th
  percentile, so a Go target no longer decays to one slot. Runs recorded before the
  history kept each run's claim size no slots.
- **A composed step claims the measured memory of members that declare none.** Before,
  only a chain declaring nothing anywhere fell back to the scheduled target's own peak.
- **A build budget refusal says how the slots were sized.** It names them declared,
  measured over N runs, unmeasured, or taken for the memory claim, beside how the memory
  was sized.
