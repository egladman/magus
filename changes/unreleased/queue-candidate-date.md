### Fixed

- **Queue candidates are dated from the commits they merge.** They carried a fixed
  2000-01-01 date, so a build that reads HEAD's date as "now" dropped every dated post.
  The plan records the newest date among its base and admitted heads, so the same queue
  still builds the same candidates.
