### Fixed

- **Records say what happened.** `config cache import --remote -o json` sets `restored` only when
  a spell restored, and `queue ls -o name` prints change ids. `graph push -o json` says `pushed`,
  not `done`. `magus\job.forkFacts` fails with no output store, and a dirty test run is no full
  test. A held jj checkout follows a descendant checkpoint.
