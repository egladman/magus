### Fixed

- **A hook glue that fails before it answers refuses its call.** `magus buzz -s` handed a
  host's hook event now exits 2 when its script is missing or fails, naming the cause, the
  rebuild and `magus agent harness install`. A stop still ends with 1. Hook lines installed
  without the `fail_closed` wrapper no longer run calls unjudged.
