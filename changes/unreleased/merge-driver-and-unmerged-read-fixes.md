### Fixed

- **The merge driver says when it drops a side.** git prints only `Auto-merging` for a
  generated file the driver keeps one side of, and a cherry-pick commits it at once. The
  driver now warns on standard error, naming the file and `magus vcs resolve`. A file
  whose regeneration cannot be recorded, as off git, stays conflicted with markers.
