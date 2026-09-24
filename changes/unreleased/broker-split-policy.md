### Added

- **`broker: required | best-effort | off` decides what a run does without a broker.**
  `required` refuses a step (MGS3022, exit 69), `best-effort` (the default) runs
  unarbitrated and says so once, and `off` never starts or contacts one. Also
  `--broker` and `MAGUS_BROKER`; Go callers pass `magus.WithBroker` or
  `magus.WithBrokerPolicy`.
