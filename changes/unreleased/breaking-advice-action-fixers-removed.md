### Removed

- **Breaking: the advice action's `fix-generated-drift`, `fix-merge-conflict`,
  `fix-label` and `offer-fix-label` inputs.** The action no longer pushes to a pull
  request, so it never needs `contents: write`; delete those keys from `with:`.
  `magus queue` settles conflicts in generated files at merge time, in a job that runs
  no pull request code, and `magus vcs resolve` settles them locally.
