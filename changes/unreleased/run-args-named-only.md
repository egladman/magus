### Fixed

- **Args after `--` reach only the target you name.** A cached replay's gates, `--preflight`
  steps and the settle step no longer receive them, so `magus run test . -- -run X` stops
  failing in its generate step with `flag provided but not defined: -run`.
