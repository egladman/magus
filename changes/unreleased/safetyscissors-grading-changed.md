### Changed

- **Breaking: a check goal names the bases it accepts.** `job.Goal` gains `accept`
  (`witnessed`, `attested`, `reported`); none, or an unknown one, is a `JobGoalInvalid`
  problem. magus resolves an unnamed accept to `[witnessed]`, and the next store migration
  writes it once onto every stored check goal.
