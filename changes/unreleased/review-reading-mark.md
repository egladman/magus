### Added

- **Mark a review as being read, and hear about a merge that lands under you.** The console's
  review route takes a `reading` op (`on: true` or `false`). It records a local mark for the
  open review and answers with `command`, the `gh pr comment` line you can run to tell the
  pull request's participants; magus prints it and never runs it, and it is empty for any
  forge but GitHub. The mark changes nothing about the merge queue. When the review merges
  while the mark is set, the `check-review` job reports `review.merged` even if nobody
  commented, then clears the mark. Two opt-in telemetry instruments record the cost:
  `magus.review.merged_while_reading` and `magus.review.merged_while_reading.duration`.
