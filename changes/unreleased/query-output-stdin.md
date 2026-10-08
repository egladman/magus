### Added

- **`magus query output --stdin` prints output records read from stdin.** `magus query
  output <ref> -o jsonl` writes a run's output record; `--stdin` prints the output and
  provenance of each record it reads and writes nothing. CI failures piped from
  `hack/ci/show-failures.buzz` read this way.
