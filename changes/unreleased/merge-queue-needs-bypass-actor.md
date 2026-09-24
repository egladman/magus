### Changed

- **The merge queue needs no bypass actor.** Apply posts `success` right before a merge,
  once main is still at the predicted tip, and GitHub's auto-merge merges; apply merges
  itself after a minute. A success it cannot follow through goes back to `pending`.
  Breaking for providers: `list_green` is required, and `merge_change` reports
  `by_provider`, which `merged` events carry.
