### Changed

- **`magus vcs ls-files` replaces `magus vcs add`.** It lists the dirty paths a commit
  wants and stages nothing; you pipe the list to `git add --pathspec-from-file=-`. Each
  group left out has an `--include-*` flag. `--untracked` and `--reason` are gone.
