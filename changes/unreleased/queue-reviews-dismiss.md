### Added

- **`magus queue reviews` says whether each approval still covers its change's head, and
  `--dismiss` dismisses the ones that do not.** It uses the queue's classifier, a code
  owner's approval included, and names the changed files. It dismisses nothing when the
  head moved. `-o json` prints a `mergequeue.reviews/v1` document. The GitHub provider
  gains the optional `reviews` and `dismiss_review` ops.
