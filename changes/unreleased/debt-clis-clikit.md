### Changed

- **Breaking: `proofread`, `proofread-train`, `train` and `embed` are built on `libs/clikit`.**
  Flags, subcommands and exit codes stay, including proofread's 0 for no deny finding, 1
  for a deny finding and 2 for an unusable run. `-h` prints the command's help to stdout and
  exits 0, where `proofread -h` and `proofread-train -h` printed to stderr and exited 2. A
  missing or unknown subcommand prints one line naming `-h` instead of the usage, and a
  flag a command does not declare exits 2 with that line.
- **Breaking: `proofread` and `embed` read a float or seed flag as text.** `--minimum`,
  `--seed`, `embed texts --cornstack`, `embed teacher-parity --min-cos` and the
  `embed train` seed and float flags report `want a grade from 0 to 100`, `is not a
  number` or `is not a whole number` where the flag package reported `invalid value`.
- **Breaking: `embed texts` refuses an operand** instead of ignoring it.
- **The `clikitmain` baseline is empty for `libs/proofread` and `libs/train`.**
