### Changed

- **A worker may merge its base into its own branch.** In its own checkout it runs
  `git merge origin/main`, or the revision its job was handed, and `--continue` or
  `--abort`. Any other merge stays refused. An orchestrator's refusal now names
  `hack/dev/merge-base-into-branches.buzz` beside the job-branch merge script.
