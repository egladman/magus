### Added

- **`magus\guard.step(kind, fun)` registers a workspace rule for one kind of step.** The
  root magusfile judges the steps an embedder's `Magus.Judge` is asked about, each kind
  with its own `fun(req: StepRequest) > GuardVerdict`. It runs after the built-in rules
  and the Go `StepRule`s, beside its approved copy, so a loosening waits on the authority.
  An unnamed verdict reports as `workspace:step`. A rule that fails judges nothing and
  says so. A row only that rule judges is then denied under `unjudged-step`.
  [ADR 0046](https://eli.gladman.cc/magus/decisions/0046-the-judge-takes-any-agent-step/).
- **A step row nothing else judges opens when the root magusfile has a rule for its
  kind.** `Open` and `Inspect` check the rows once the magusfile has loaded.

### Changed

- **`step` is a reserved guard rule name**, as `command`, `spawn`, `write` and `stop`
  are. A verdict or a step kind named `step` is refused (MGS1045 in a magusfile).
- **`magus.WithStepRows`, `magus.WithStepRule` and `magus.WithSpawnRule` panic when
  applied to a bare `workspace.Load`**, which reads none of them, instead of setting
  nothing.
