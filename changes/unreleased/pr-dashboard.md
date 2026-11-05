### Added

- **A Pulls app in the console shows the open pull requests against the base branch.** The
  server polls the review provider and serves the last board it read. Each row carries CI
  state, the first failure, why the pull cannot merge, and a suggested merge order. Merge-queue
  status is its own column, never CI. Checks queued past
  `console.pulls.runner_wait` are named.
- **`console.pulls` sets the poll cadence.** `floor` (30s) is the shortest wait, `active`
  (1m) applies while checks run, `idle` (5m) otherwise, and `reserve` (500) is the request
  budget kept back: below it the poller waits for the reset and the board says so.
  `runner_wait` (15m) and `stale_after` (72h) set the board's thresholds.
- **The board shows your own pull requests until you pick authors.** The choice is saved in
  `authors.jsonl` in the repository's state directory; an empty choice shows every author.
