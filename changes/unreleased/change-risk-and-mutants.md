### Added

- **A function a change touches is scored for change risk and mutated.** Spells declare
  decision tokens, token swaps and a coverage command as data; `Magus.ChangeRisk` and
  `Magus.Mutants` answer for a change, and Buzz reads them as `change(base).risk()`,
  `change(base).mutants()` and `workspace().risk()`.
  `// magus:mutant op=<swap> reason=<why>` excepts an equivalent mutant
  ([ADR 0023](docs/decisions/0023-touched-functions-meet-change-risk-and-their-mutants.md)).
