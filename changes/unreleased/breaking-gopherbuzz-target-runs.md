### Changed

- **Breaking (Go API): gopherbuzz's `TargetMemo` is `TargetRuns`.** `NewTargetRuns(done ...)`
  takes the targets already done, and `MarkDone` records more later. `WithTargetRuns` and
  `TargetRunsFromContext` replace the memo spellings.
