### Changed

- **Breaking: a job's write, read, deny and soft paths nest under `paths`.** A
  `magus job fork --stdin` or `magus job apply` record names
  `"paths": {"write": [...], "read": [...], "deny": [...]}` in place of `write_paths`,
  `read_paths` and `deny_paths`, and a record naming a flat key is refused as an unknown
  member. `magus ls jobs -o json` and `magus describe job -o json` print `paths.write`,
  `paths.read`, `paths.deny` and `paths.soft`, and a problem names its field the same
  way (`paths.write[2]`). The flags keep their names: `--write-paths`, `--read-paths`,
  `--deny-paths`, `--add-write-path` and `--release-write-path`.
- **Breaking: the stored `batch.magus/v1alpha1` row nests them as `spec.paths`.** Store
  migration 7 moves `spec.writePaths`, `spec.readPaths`, `spec.denyPaths` and
  `spec.softPaths` under `spec.paths`. An older magus cannot read a store this one
  migrated.
- **Breaking: `magus\Job` carries `paths`, a `magus\JobPaths`.** A script reads
  `job.paths.write` where it read `job.writePaths`, and
  `magus\workspace().putJob(id, opts: {"paths": {"write": [...]}})` replaces
  `write_paths`. A put still never sets `paths.soft`.
- **Breaking: the console API's `Job` nests its paths in a `JobPaths` message.**
  `magus.job.v1alpha1.Job.paths` (field 47) replaces `write_paths`, `deny_paths` and
  `read_paths`, whose field numbers 13 to 15 are reserved, and it carries `soft` too.
