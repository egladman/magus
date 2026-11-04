### Added

- **A function a change touches is scored for change risk and mutated.** Spells declare
  decision tokens, token swaps and a coverage command as data; `Magus.ChangeRisk` and
  `Magus.Mutants` answer for a change, and Buzz reads them as `change(base).risk()`,
  `change(base).mutants()` and `workspace().risk()`.
  `// magus:mutant op=<swap> reason=<why>` excepts an equivalent mutant
  ([ADR 0023](docs/decisions/0023-touched-functions-meet-change-risk-and-their-mutants.md)).
- **Each touched function is judged against itself at base.** Its change risk and its
  surviving mutants are measured again in an export of the merge base, and a function is
  refused only when it is worse there too, so shrinking a function never costs. Lint
  runs both rules.
- **Coverage floors hold the covered lines a change keeps.** A line covered at base that
  is still present and no longer covered, or a coverable line added uncovered, is a
  finding; removing code never is. The 70% Go and 62% Buzz workspace floors are removed.
