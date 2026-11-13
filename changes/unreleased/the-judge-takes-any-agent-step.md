### Added

- **`Magus.Judge` judges any step an embedder's agent takes.** A step is data
  (`agent.Step`: a kind, a name and a JSON input), and `magus.WithStepRows` declares
  each kind once. The built-in rules judge a row's command and paths. A
  `magus.StepRule` from `magus.WithStepRule` judges every step, and the strictest
  decision wins.
  [ADR 0046](https://eli.gladman.cc/magus/decisions/0046-the-judge-takes-any-agent-step/).
- **The `step-input` rule** denies a judged step whose input fails its row's JSON
  Schema, or holds the wrong type where the row points. An undeclared kind is denied
  under `unjudged-tool`, and a malformed row, or one nothing judges, fails `Open`.
