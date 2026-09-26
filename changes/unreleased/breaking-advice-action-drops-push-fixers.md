### Removed

- **Breaking: the advice action's `fix-generated-drift`, `fix-merge-conflict`,
  `offer-fix-label` and `fix-label` inputs.** Both fixers pushed to the pull request's
  branch and neither ever ran: their consent label never matched `gh`'s JSON. The merge
  queue now settles drift and conflicts at merge time. Delete the keys from `with:`; the
  action needs only `pull-requests: write`.
