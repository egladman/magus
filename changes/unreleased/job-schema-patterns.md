### Changed

- **`magus job fork --schema` publishes the record's shape as patterns.** A write path, a
  timeout, a checkout, a check target, and each lease id in `parent` and `depends_on` carry
  the pattern the record check enforces, so a record a schema validator accepts passes the
  record's shape checks. A check is a target with no script or a script with no target.
