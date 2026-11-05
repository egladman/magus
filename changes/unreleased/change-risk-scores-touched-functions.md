### Added

- **A function a change touches is scored for change risk.** Spells declare decision
  tokens and where their coverage profile lands as data; `Magus.ChangeRisk` answers for a
  change, and Buzz reads it as `change(base).risk()` and `workspace().risk()`. Lint
  refuses a touched function past 10
  ([ADR 0023](docs/decisions/0023-touched-functions-meet-change-risk-and-their-mutants.md)).
