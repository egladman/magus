### Changed

- **A parallel `ctx.needs` call claims the peak of its members' summed memory curves.**
  When every member recorded a curve, the claim is the largest total at any instant,
  so members whose peaks do not coincide no longer claim the sum of their peaks. A
  member with no curve keeps the summed peaks, and the sizing names it.
