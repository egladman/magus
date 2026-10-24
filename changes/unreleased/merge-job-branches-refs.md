### Changed

- **`merge-job-branches.buzz` takes any ref that resolves to a commit.** `--branches
  origin/main` no longer fails as an unknown branch; the script prints what each ref
  resolved to. An existing `--into` is still refused, and the refusal now names
  `merge-base-into-branches.buzz --branches <into>` as the script that updates it.
