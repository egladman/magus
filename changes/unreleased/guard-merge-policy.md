### Changed

- **A worker may merge its base into its own branch.** A worker holding a live job runs
  `git merge origin/main`, or a merge of the revision its job was handed, on its own
  branch in its own checkout, and finishes or drops it there with `git merge --continue`
  or `--abort`. Any other merge, and a merge in another checkout, stays refused. An
  orchestrator's `git merge` is still refused as groundwork, and the refusal now names
  `magus buzz hack/dev/merge-base-into-branches.buzz -- --branches <branch>` beside the
  job-branch merge script.
