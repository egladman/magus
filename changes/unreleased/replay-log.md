### Added

- **`magus buzz --record` keeps every call the script makes.** Each call is
  appended to `replay/<ref>/calls.jsonl` beside the output store, begun and
  synced before it runs. A failed run's `ref` line adds `died in call #<seq>
  <name>`. A `--test` run keeps none. The header lists no imported files yet.
- **A rerun picks up where a dead run stopped.** Run the same command again after a
  reboot or a Ctrl-C and the completed calls are answered from the record, not run
  again. `resuming <ref>: <n> calls on record` prints first. A run that reached
  its end writes a footer, and its rerun starts a new attempt.
- **A rerun refuses what it cannot replay, and names the call.** A different call, an
  unrecorded result, a module whose results live in the process and an interrupted call
  that is not a read each stop the run. `--restart` discards the record and starts from
  the top.
