### Changed

- **The pull request guard runs CI's hygiene checks before a pull request opens.**
  It denies a pull request whose changelog fragment is owed, missing or malformed, and one
  whose diff it cannot read. `hack/policy/gen/proofread-decisions.json` is generated from
  `hack/policy/proofread.buzz`, and a test holds `.github/workflows/pr.yaml` to the guard's
  list of hygiene targets.
