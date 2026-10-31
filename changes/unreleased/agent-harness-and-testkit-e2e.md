### Changed

- **Breaking: the `hoste2e-test` target is now `agent-harness-test`, with no alias.** The suite moved
  to `internal/agent/harness` as one testify suite per host. Its report's `results` rows became
  `cases`, keyed `subject` and `case`. The generic pieces moved to the new `libs/testkit/e2e`.
