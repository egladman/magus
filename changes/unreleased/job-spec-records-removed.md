### Removed

- **Breaking: `agents.brief_format` and the Buzz type check of a spawn prompt.** A spawn
  prompt is the parent's own words and the job row is the record (ADR 0043), so there is
  no format to choose: a `magus.yaml` still setting the key fails as an unknown key, and
  `magus doctor` drops its brief-format check.
