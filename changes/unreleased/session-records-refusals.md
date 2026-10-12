### Added

- **`magus session` records a run the machine refused.** A step turned away because the
  machine's build budget was full (MGS3009) is now a failed target in the session store,
  with its code (`diagnostic_code` in JSON), so `magus session` and `--brief` show the
  refusal. A refusal has no output ref because nothing ran.
