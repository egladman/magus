### Changed

- **A target that declares nothing claims what its runs measured.** After three measured
  runs, its step claims 1.25 times their highest peak memory and, as slots, the 75th
  percentile of CPU time over wall time, rounded up. A declared `memory_mb` caps the claim
  and a declared `slots` wins. A refusal and `magus session` say when a claim was measured.
