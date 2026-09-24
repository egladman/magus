### Fixed

- **Three guard rules match their catalog entries.** `cd` fires only ahead of a magus
  command. `cache-dir-write` grades only write targets, so `rsync --exclude .magus` and
  an interpreter's quoted data pass. `stage-all`'s description now names `-u`, `.` and
  the long forms its matcher already covered.
