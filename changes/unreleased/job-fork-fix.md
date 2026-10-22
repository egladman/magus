### Added

- **`magus job fork --fix` and `magus job apply --fix` make the edits a problem names without judgment.** An output regenerated outside the write paths (MGS3044) is added to them, or waived for the integrator when no write path can spell it; a ci gate nothing runs (MGS3045) gets its goal, lint's without default charms. Each round validates again until no edit is left, each edit is printed, and only the problems that need a person stay listed. With `--checkout`, `--fix` also moves a checkpoint the forker cannot place (MGS3041) to its HEAD. Without `--fix`, the problem list says how many it would apply, and each problem's JSON carries its `edit`.

### Changed

- **A `magus job fork --stdin` record that names no `schema_version` is read as the current version, with a note saying so.** A record naming version 0 is still refused, and `magus job apply` records still name their version.
