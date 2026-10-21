### Changed

- **A parallel `ctx.needs` call claims the peak of its members' summed memory curves.**
  When every member's runs recorded a curve, the members are laid out over time from
  the call's start and the claim is the largest total at any instant, so members whose
  peaks do not coincide no longer claim the sum of their peaks. A call with a member
  that has no curve still claims the summed peaks, and the claim's sizing names that
  member, as a single claim with too few curves names the runs it had.
