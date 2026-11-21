### Changed

- **Breaking: `proofread`, `proofread-train`, `train` and `embed` are built on `libs/clikit`.**
  Flags, subcommands and exit codes stay. `-h` now prints help to stdout and exits 0;
  `proofread` and `proofread-train` previously used stderr and exited 2. Missing or
  unknown subcommands and undeclared flags print one line naming `-h` instead of usage.
- **Breaking: `proofread` and `embed` read a float or seed flag as text.** `--minimum`,
  `--seed`, `embed texts --cornstack`, `embed teacher-parity --min-cos` and the
  `embed train` seed and float flags report `want a grade from 0 to 100`, `is not a
  number` or `is not a whole number` where the flag package reported `invalid value`.
- **Breaking: `embed texts` refuses an operand** instead of ignoring it.
- **The `clikitmain` baseline is empty for `libs/proofread` and `libs/train`.**
