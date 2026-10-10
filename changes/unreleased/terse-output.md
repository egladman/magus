### Changed

- **Breaking: magus prints an agent the verdict, one command and a ref.** Guard denials,
  advisories and diagnostics keep their reasons behind `magus query output <ref>`; a
  person still sees them. Set `log.audience`; the harness config sets it for agents.
  `session --brief -o json` renames `leases_omitted` to `other_leases`, and a job's
  checkpoint must name a real revision.
