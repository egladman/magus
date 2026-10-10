### Added

- **The drift notice counts the unread hunks of the range a push sends.** The `check-drift`
  job the managed `pre-push` section hands off to adds one line naming how many are unread and
  the `magus diff --unread` command that opens them. Nothing new runs in the hook, and nothing
  blocks the push.
