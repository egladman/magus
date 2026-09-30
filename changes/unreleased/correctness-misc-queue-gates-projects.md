### Fixed

- **The merge queue gates the change's projects.** It appended them after the gate
  line's `--`, so `run ci:gha -- --inherited=fatal <projects>` forwarded them as
  arguments, selected every project, and failed `security`'s argument check. The
  units now go ahead of the line's first `--`.
