### Fixed

- **Merge-queue hooks can run what they build in their scratch directory.** A hook's
  scratch directory, where its tool caches live, is now read, write and execute, so a
  regeneration's `go run` no longer fails with `permission denied` on the binary Go cached there.
