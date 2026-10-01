### Added

- **A spell declares how its language writes a placeholder body.** The `Language` record
  gains `syntax.stubs`: the SCIP symbol kinds a stub may replace, whether the body is
  brace- or indent-delimited, and a Mustache body template with `Name`, `Qualified`,
  `Kind` and `Branch` holes. The go, typescript, rust and python spells declare one; an
  unknown style or kind fails to load.
