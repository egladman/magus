### Fixed

- **The lease guard no longer dies on a script that calls itself.** It refuses scripts
  nested past its bound, reads a script from the directory a `cd` reached, and grades
  `git commit --pathspec-from-file <file>`. A worker's `./magus` build stays refused with
  no command rule. `MissingCommits` also compares a same-subject commit's changed lines.
