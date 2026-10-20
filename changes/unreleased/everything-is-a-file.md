### Changed

- **The doctrine states "Everything is a file".** Local state is JSON Lines: one append-only `.jsonl` file per store, one JSON object per line, the last line for a key wins, compaction by rename, the encoding on the entry type. `cat`, `grep` and `diff` read it. It lists the stores not yet following it, the job store and runtime history first.
