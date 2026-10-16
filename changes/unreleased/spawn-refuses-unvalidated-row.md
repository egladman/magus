### Changed

- **The guard refuses a spawn for a job whose record was never validated.** A spawn titled
  `<parent>/<role> <job>` naming a live row with no `validated` stamp is denied as
  `spawn-unvalidated`, with `magus job fork <job> --dry-run` to read its problems and
  `magus job apply -f <record>` to validate it. Rows an older magus wrote carry no stamp
  and are re-applied once.
