### Changed

- **`merge-job-branches.buzz --apply` recomposes the landing branch's commits.** After
  merging and regenerating, the commits above the base become one commit per merged job
  branch, in merge order, with no merge commit. Each declared output goes with the first
  job whose source it reads. Commits a remote ref already holds stay, and a tree that
  differs after recomposing fails the merge.
