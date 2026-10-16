### Added

- **`magus job fork --dry-run` validates a job record without forking it.** It prints every problem at once, each as `[MGSxxxx] <field>: <message>` with a `fix:` line, then the record as it would be stored, the problems a waiver accepted and the checks that could not run. It writes nothing and exits 1 while a problem remains. `-o json` emits the same as `types.JobValidation`. A real fork with problems prints the same list, and `job apply` lists every record's problems before it writes any.

### Changed

- **The outputs a job's target regenerates outside its write paths, and the ci targets it does not run, are problems of the record, not notes.** A fork or apply that would have printed them now refuses with MGS3044 or MGS3045 unless the record adds the output or goal, or waives it in `waivers`.
