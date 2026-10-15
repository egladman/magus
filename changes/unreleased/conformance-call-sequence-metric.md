### Added

- **The `call-sequence` conformance metric.** The longest ordered run of workspace callees a function shares with any other, measured for `source` and `main` and judged like every conformance metric. Its anchor is 7: a shared run of 8 callees is where the review that measured it found its true positives, not a percentile.
