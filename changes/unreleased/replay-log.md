### Added

- **`magus buzz --record` keeps every call the script makes.** Each call is
  appended to `replay/<ref>/calls.jsonl` beside the output store, begun and
  synced before it runs. A failed run's `ref` line adds `died in call #<seq>
  <name>`. A `--test` run keeps none.
- **Only a call that may not repeat is synced to disk before it runs.** A read's
  begin line and every end line ride the next sync. On an M5 Mac ten thousand reads
  record in a third of a second, one exec in five in about 9 seconds, and ten thousand
  execs in about 42.
- **The record holds the sha256 of every file the script imported.** It is
  written once the script compiles, before its top level runs.
- **A rerun picks up where a dead run stopped.** Run the same command again after a
  reboot or a Ctrl-C and the completed calls are answered from the record, not run
  again. `resuming <ref>: <n> calls on record` prints first. A run that reached
  its end writes a footer, and its rerun starts a new attempt.
- **`magus session --brief` lists interrupted recorded runs.** Each comes with the
  command that resumes it and the `--restart` command that starts it over. The brief
  prints them and starts nothing; with none, it adds nothing.
- **`magus query output <ref>` shows a recorded run's calls.** The last 20 follow the
  run's output as a notice, the open call marked, with the path of `calls.jsonl` that
  holds them all.
- **A rerun refuses what it cannot replay, and names the call.** A different call, an
  unrecorded result, a live module, an interrupted call that is not a read and a
  changed import each stop the run. `--restart` discards the record and starts from
  the top.
