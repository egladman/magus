### Changed

- **Breaking: a check postcondition names the bases it accepts.** `job.Postcondition` gains `accept`
  (`witnessed`, `attested`, `reported`); none, or an unknown one, is a `JobPostconditionInvalid`
  problem. magus resolves an unnamed accept to `[witnessed]`, and the next store migration
  writes it once onto every stored check postcondition.
