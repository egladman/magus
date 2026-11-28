### Added

- **`magus buzz --record` keeps every call the script makes.** Each call is
  appended to `replay/<ref>/calls.jsonl` beside the output store, begun and
  synced before it runs. A failed run's `ref` line adds `died in call #<seq>
  <name>`. A `--test` run keeps none.
- **Recording costs 4 to 5 ms a call on an M5 Mac.** Nearly all of it is the sync,
  so a run of ten thousand calls spends 40 to 48 seconds recording them.
- **The record holds the sha256 of every file the script imported.** It is
  written once the script compiles, before its top level runs.
- **A rerun picks up where a dead run stopped.** Run the same command again after a
  reboot or a Ctrl-C and the completed calls are answered from the record, not run
  again. `resuming <ref>: <n> calls on record` prints first. A run that reached
  its end writes a footer, and its rerun starts a new attempt.
- **A rerun refuses what it cannot replay, and names the call.** A different call, an
  unrecorded result, a live module, an interrupted call that is not a read and a
  changed import each stop the run. `--restart` discards the record and starts from
  the top.
