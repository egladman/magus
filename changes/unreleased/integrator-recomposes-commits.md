### Changed

- **`merge-job-branches.buzz --apply` recomposes the landing branch's commits.** After
  merging and regenerating, the commits above the base become one commit per merged job
  branch, in merge order, each declared output with the first job whose source it reads,
  and no merge commit. A subject joins the job's heaviest worker clauses within 72
  characters. Commits a remote ref of the branch holds stay, and a tree that differs
  after recomposing fails the merge.
