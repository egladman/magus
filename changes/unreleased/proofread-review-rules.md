### Changed

- **proofread accepts "the doc says X, this does Y" as the reason for a requested change, and flags long
  review comments.** `no-reason` stops firing on a comment that sets what was meant against what the code
  does. The new `review-length` rule advises on an inline comment over 60 words, a summary over 100 and a
  reply over 80. It ships off, because no round has measured it.
