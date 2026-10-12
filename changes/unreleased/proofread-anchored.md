### Added

- **`proofread review-reply -anchored` judges an inline comment as one.** A comment
  posted on a line of the diff, or a reply in its thread, refers to that line, so
  `nonspecific` leaves it alone and judges top-level comments only. The guard passes the
  flag for the `pulls/N/comments` and `.../replies` endpoints.
