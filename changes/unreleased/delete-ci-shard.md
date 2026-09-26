### Removed

- **Breaking: the `ci-shard` target.** A workflow publishes the plan itself: save
  `magus affected ci --plan` to a file, then redirect `magus run --stdin --dry-run -o
  'template=...' < plan.json` into `$GITHUB_OUTPUT` and `$GITHUB_STEP_SUMMARY`. The GitHub
  Actions guide shows the three lines. A magusfile that copied `ci_shard` keeps working.
