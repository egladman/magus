### Changed

- **BREAKING: the in-repo analyzers carry no repository policy.** `hostagnostic`
  takes its host list from `hosts` and `host-patterns`, and six conventions analyzers
  take a `hint` for the repository-specific half of their message.
