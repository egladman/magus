### Fixed

- **A convention check compares a renamed file with its old path.** The shared
  base diff followed no rename, so an unchanged renamed file counted as new code
  and `blankerror` charged its whole content. Git's rename detection now maps
  each file to its path at the merge base.
