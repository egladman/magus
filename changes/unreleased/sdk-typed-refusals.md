### Changed

- **Breaking for SDK callers: the spawn rule `WithSpawnRule` installs now runs.** The
  guard's spawn ceiling and every job fork ask it in place of a fixed tier compare, so a
  rule of your own is enforced. `TierCeiling` refuses with
  [MGS3055](https://eli.gladman.cc/magus/reference/codes/sandbox/MGS3055/) or
  [MGS3056](https://eli.gladman.cc/magus/reference/codes/sandbox/MGS3056/), which
  `errors.Is` matches.
