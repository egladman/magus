### Changed

- **Breaking: `log.format: jsonl` is refused** from `magus.yaml`, `MAGUS_LOG_FORMAT` and
  `--log-format`. It withheld per-target results with no stream to carry them; `-o jsonl`
  is the one way to select it.
