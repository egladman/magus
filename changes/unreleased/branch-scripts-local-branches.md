### Changed

- **`hack/dev/rebase-branches-onto-base.buzz` and `hack/dev/merge-base-into-branches.buzz` accept local branches.** The rebase script fetches only the branches origin has, reads the rest from their local ref and pushes them new with `-u`. `merge-base-into-branches.buzz` merges the base into each branch independently; `--stack` restores the old chaining. It works in the worktree that has a branch checked out instead of failing on `git switch`, commits a settled merge a stopped run left behind, and every failed git call prints its stderr.
