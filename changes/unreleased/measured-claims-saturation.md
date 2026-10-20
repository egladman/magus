### Changed

- **A measured slot claim grows when the claim held the run back.** A run that kept 90%
  of its slots busy is saturated, and the next claim doubles, up to the budget, until a
  run granted more comes back unsaturated. Only unsaturated runs feed the 75th
  percentile, so a Go target no longer decays to one slot.
