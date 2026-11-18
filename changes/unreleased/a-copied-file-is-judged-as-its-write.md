### Added

- **`copy-write` judges a file copied in from scratch as the write it is.** `cp`, `mv` and
  `install` from outside the workspace into it read the source and meet the comment,
  lease and workspace write rules, as a heredoc's body does. A source over 1 MiB or
  unreadable is refused; a binary source is judged by path.
  [copy-write](https://eli.gladman.cc/magus/reference/rules/copy-write/).
