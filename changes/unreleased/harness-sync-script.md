### Added

- **`hack/dev/sync-harnesses.buzz` installs and merges each wired harness's host config.**
  It runs `agent harness install`, applies the `describe harness` plan idempotently, and
  prints a line per file changed. It refuses while the harness spells, agent docs or guard
  policy differ from HEAD. `bootstrap-worktree.buzz` runs it when no lease is bound.
