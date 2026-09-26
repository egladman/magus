### Fixed

- **Merge-queue hooks can run what they build in their caches.** A hook's tool caches
  live in its checkout's `.magus`, which the workspace grant makes read, write and
  execute, so a regeneration's `go run` no longer fails with `permission denied` on the
  binary Go cached there.
