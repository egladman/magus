### Added

- **A ledger of work nothing accounts for.** `internal/work` surveys every registered checkout,
  gone ones included, and every branch holding unpublished revisions, recording each in
  `$XDG_STATE_HOME/magus/work/<repo>/work.jsonl` as clear, absorbed, tracked, local-only, ended
  or unaccounted. Stashes and preserved captures are surveyed where the backend lists them, and
  the survey says so where it does not.
- **A new user-level key, `work.local_only`, names work never meant to be published.** A
  repository config that sets it is refused. `hack/dev/ls-worktrees.buzz --unaccounted` lists the
  unaccounted items.
