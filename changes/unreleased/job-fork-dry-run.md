### Added

- **`magus job fork --dry-run` validates a job record without forking it.** It prints every problem as `[MGSxxxx] <field>: <message>` with a `fix:` line, then the record as it would be stored, permitted problems and checks that could not run. It writes nothing and exits 1 while a problem remains; `-o json` emits `types.JobValidation`.
