### Changed

- **A target that declares nothing claims what its runs measured.** Once a target has
  three successful measured runs, a step claims 1.25 times their highest peak memory and,
  as slots, the 75th percentile of CPU time over wall time, rounded up. Before, an
  undeclared target claimed no memory and one slot. A declared `memory_mb` stays the
  ceiling and a declared `slots` wins outright. A refusal and `magus session` say when a
  claim was measured.
- **A one-slot step runs the Go toolchain one wide.** Every step now sets `GOMAXPROCS`
  and `-p` to the slots it claims. Before, a one-slot step left Go its defaults, so it ran
  on every core while the budget counted one. A Go target that needs more declares
  `slots`, since its measured width cannot grow past its claim.
