### Changed

- **`merge-job-branches.buzz --apply` recomposes the landing branch's commits.** After
  merging and regenerating, the commits above the base become one commit per project,
  each declared output with its source, and no merge commit. Subjects join the workers'
  heaviest clauses. Commits a remote ref of the branch holds stay, and a tree that
  differs after recomposing fails the merge.
