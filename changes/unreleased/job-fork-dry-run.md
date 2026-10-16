### Added

- **`magus job fork --dry-run` validates a job record without forking it.** It prints every problem as `[MGSxxxx] <field>: <message>` with a `fix:` line, then the record as it would be stored, waived problems and checks that could not run. It writes nothing and exits 1 while a problem remains; `-o json` emits `types.JobValidation`.

### Changed

- **The outputs a job's target regenerates outside its write paths, and the ci targets it does not run, are problems of the record, not notes.** A fork or apply that would have printed them now refuses with MGS3044 or MGS3045 unless the record adds the output or goal, or waives it in `waivers`.
