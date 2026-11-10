### Changed

- **A pull request here shows seven fewer checks.** Queue advice's jobs and its pending
  `merge-queue` status are gone; only the queue posts that status. Pull request events
  run `queue-intent.yaml`, so `queue.yaml`'s skipped jobs no longer show. The landlock
  test runs as the cached `landlock-test` target, and `deliberate breaks` runs only when
  a file it lists changes.
