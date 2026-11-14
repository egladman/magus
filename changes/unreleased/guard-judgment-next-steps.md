### Changed

- **Breaking: a guard judgment's remedy is `next_steps`, not `next`.** The Go field
  `types.GuardJudgment.Next` is `NextSteps`, with no alias. The hook glues read neither,
  so no installed template changes.
  [ADR 0046](https://eli.gladman.cc/magus/decisions/0046-the-judge-takes-any-agent-step/).
