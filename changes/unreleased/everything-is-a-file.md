### Changed

- **The doctrine states "Everything is a file".** Local state is JSON Lines: one append-only
  `.jsonl` file per store, one JSON object per entry per line, the last line for a key wins,
  compaction by rename, and the encoding on the entry type rather than in a reader. It is
  readable with `cat`, `grep` and `diff`, never one rewritten JSON document. The entry lists the
  stores that do not follow it yet, the job store and runtime history first.
