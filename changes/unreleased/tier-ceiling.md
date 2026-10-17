### Added

- **A child job never outranks its parent's model tier.** `magus job fork` refuses a row whose tier ranks above the tier its parent row was forked at, by declaration position, whichever step chose it: the row, the matrix or the default. The refusal names both tiers, the `--model` that fits, and the way up: a person forks the higher-tier row.
