### Added

- **A merge-queue provider's `describe` can decline with `{refused: {reason, url, app}}`.**
  `magus queue describe` prints what is missing and where, then the command to run next
  with the provider's `app` in place of `--app`. The queue still passes `--app` to the
  provider unread.
