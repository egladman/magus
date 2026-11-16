### Changed

- **`step` is a reserved guard rule name**, as `command`, `spawn`, `write` and `stop`
  are. A verdict or a step kind named `step` is refused (MGS1045 in a magusfile).
- **`magus.WithStepRows`, `magus.WithStepRule` and `magus.WithSpawnRule` panic when
  applied to a bare `workspace.Load`**, which reads none of them, instead of setting
  nothing.
