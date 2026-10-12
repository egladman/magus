### Added

- **`testkit.NewRepo` builds a committed git repository on `main` for a test.** Its config
  stops background maintenance and sets an identity, and `Repo.Git` runs git without the
  caller's global config or a hook's `GIT_DIR`, so it needs no `testkit.Main`. `Commit`,
  `Head` and `AddWorktree` cover the usual fixture steps.
