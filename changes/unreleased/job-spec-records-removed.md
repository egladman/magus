### Removed

- **Breaking: `agents.brief_format` and the Buzz type check of a spawn prompt.** A spawn
  prompt is the parent's own words and the job row is the record (ADR 0043), so there is
  no format to choose: a `magus.yaml` still setting the key fails as an unknown key that
  names ADR 0043, and `magus doctor` drops its brief-format check.
- **Breaking: `magus job fork --brief-format`, a row's `brief_format` and `magus\Brief`.**
  A record sending `brief_format` is refused as an unknown member, and a stored row
  carrying it reads without it. `magus\Brief`, `BriefDecision`, `BriefRef` and
  `BriefRefKind` leave the Buzz declarations; the row's `JobDecision` and `JobEvidence`
  replace them.
