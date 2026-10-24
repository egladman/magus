### Added

- **The guard speaks of a background task that has gone silent.** On each shell call of a
  root session, a background task that has written nothing for 30 minutes is named once,
  with the `TaskStop` call that ends it; past 60 minutes every shell call that is not a read
  is refused until it is stopped. The rule is `background-silent`.
- **A background stop loses nothing it does not name.** `background-silent` names each
  task's output file, which a stop leaves on disk, and how to rerun the work bounded. magus
  never stops a task itself, and reads only what the transcript gained since its last call.
- **The background bounds are settings.** In `magus\guard.builtins`,
  `background-unbounded` takes `ceiling_minutes` and `background-silent` takes
  `advise_minutes` and `deny_minutes` (30, 30 and 60 unset; the deny must be past the
  advice).

### Changed

- **`filter-without-input` catches interpreters waiting on stdin.** `python3 -`, `node`,
  `perl -`, `ruby`, `bash -s`, a shell with no script, and `read` without `-t` or `-u` are
  refused when nothing on the line feeds their stdin, and the refusal names the heredoc to
  put on that command.
- **`background-unbounded` names a ceiling.** The refusal offers `timeout 30m <command>`
  or `magus job exec --timeout 30m -- <command>`, at the workspace's `ceiling_minutes`.
