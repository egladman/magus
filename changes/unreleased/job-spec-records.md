### Added

- **`stop-unfiled` refuses a leased subagent's first stop until it files its job result.**
  The refusal names `magus job exit <job> --stdin` and `magus job exit --schema`; a
  read-only row files one too, with an empty `changed_paths`. The second stop passes with
  a note, so nothing re-prompts the child, and `magus job wait` shows the row never
  passed. Set it to `advise` or `off` through `magus\guard.builtins`.

### Changed

- **Breaking: the spawn-time rules are renamed.** `brief-command` is now `spawn-command`,
  `brief-plain-commands` is `spawn-plain-commands`, and `spawn-unbriefed`,
  `buzz-unbriefed` and `architecture-unbriefed` are `spawn-before-skill`,
  `buzz-before-skill` and `architecture-before-skill`. A `magus\guard.builtins` that
  still names an old one fails to load, naming the new one.
- **`magus job exec` ends by saying to read the spec** (`magus describe job <id>`) rather
  than the terms.

### Removed

- **Breaking: `agents.brief_format` and the Buzz type check of a spawn prompt.** A spawn
  prompt is the parent's own words and the job row is the record (ADR 0043), so there is
  no format to choose: a `magus.yaml` still setting the key fails as an unknown key, and
  `magus doctor` drops its brief-format check.
