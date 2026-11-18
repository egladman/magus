### Changed

- **Breaking: `magus run <target> --all` replaces the `/` operand.** `--all` selects every
  project from any directory. A `/` operand, or `--all` beside named projects, is refused
  with a usage error. A shell completes `/` into `/usr` and `/etc`. The merge queue hands
  its hooks `--all` where it handed `/`.
