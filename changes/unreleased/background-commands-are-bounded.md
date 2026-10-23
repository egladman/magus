### Added

- **The guard speaks of a background task that has gone silent.** On each shell call of a
  root session, a background task that has written nothing for 30 minutes is named once,
  with the `TaskStop` call that ends it; past 60 minutes every shell call that is not a read
  is refused until it is stopped. The rule is `background-silent`.

### Changed

- **`filter-without-input` catches interpreters waiting on stdin.** `python3 -`, `node`,
  `perl -`, `ruby`, `bash -s`, a shell with no script, and `read` without `-t` or `-u` are
  refused when nothing on the line feeds their stdin, and the refusal names the heredoc to
  put on that command.
- **`background-unbounded` names a default ceiling.** The refusal offers `timeout 30m
  <command>` or `magus job exec --timeout 30m -- <command>`.
