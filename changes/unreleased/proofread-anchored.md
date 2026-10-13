### Added

- **`proofread` judges review text as three kinds.** `review-summary` is a review's body,
  `review-inline` a comment on a line of the diff, and `review-reply` a reply in a thread.
  `nonspecific` leaves inline comments alone, since the line they sit on is their referent.
  `review-reply -anchored` still judges as `review-inline`.
