### Added

- **`magus job fork` holds a child job to its parent's model tier as `spawn-above-parent-tier` says.** A row whose tier ranks above the tier its parent row was forked at, by declaration position, whichever step chose it (the row, the matrix or the default), is advised by default. Under a deny setting the fork is refused, naming both tiers, the `--model` that fits, and the way up: a person forks the higher-tier row.
