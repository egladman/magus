### Changed

- **`magus refs -o json` carries the index cause.** The answer of `refs`, `refs
  --occurrences` and `refs --definition` holds `index_cause {why, fix}` whenever it reports
  a missing or stale index, the same sentences the text output prints. A name refs cannot
  resolve now prints its record, verdict included, under `-o json`, `-o yaml`, `-o jsonl`
  and `-o template`.

### Fixed

- **The refresh hook's `./magus` is looked for where git runs the hook.** The not-indexed
  diagnosis resolved a relative hook binary against the magus workspace root; it now uses
  the repository's top level, which differs when the workspace sits below it.
