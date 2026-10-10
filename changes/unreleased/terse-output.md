### Changed

- **Breaking: `-q` and `-s` print the verdict, one command and a ref.** Guard denials,
  advisories and diagnostics keep their reasons behind `magus query output <ref>`, and
  the default output still shows them. `session --brief -o json` renames
  `leases_omitted` to `other_leases`, and a job's checkpoint must name a real revision.
