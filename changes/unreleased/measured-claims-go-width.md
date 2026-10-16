### Changed

- **A one-slot step runs the Go toolchain one wide.** Every step now sets `GOMAXPROCS`
  and `-p` to the slots it claims. Before, a one-slot step left Go its defaults, so it ran
  on every core while the budget counted one. A Go target that needs more declares
  `slots`, since its measured width cannot grow past its claim.
