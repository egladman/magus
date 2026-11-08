### Changed

- **The broker grants slots elastically.** A claim is seated at its `slots_floor`
  and granted up to `slots`: what is free when none wait, else free / (waiters + 1),
  never under one. The verdict's `slots` names the grant; a claim without a floor
  keeps its width. `MachineBudget.Request` takes the parked count, and a parked
  verdict's `ahead` names its place.
