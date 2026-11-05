### Fixed

- **A magus nested in a sandboxed run keeps `MAGUS_HISTORY_PATH`.** The scrubbed
  environment dropped it, so the nested run wrote its run history under the box's
  discarded home, logged `failed to save volatility history`, and the shard planner never
  saw it. A value the target sets still wins.
