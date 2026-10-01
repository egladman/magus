### Changed

- **`magus job exec` refuses a job another checkout holds.** It used to move the job's
  checkout silently, so `job wait` graded the second taker's tree. The refusal names the
  holder's checkout, its state, how long ago it last moved, and how to hand the job over:
  its holder runs `magus job exit`, then its forker runs `magus job apply`.
