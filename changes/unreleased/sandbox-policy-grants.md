### Changed

- **Breaking: sandbox grants are explicit, and misconfiguration is an error.** Exec
  needs `mode: rx` or `rwx`; `ro` and `rw` no longer imply it. An unset `$VAR`, a mode
  typo or a passthrough prefix like `GO*` fails with MGS2004. System, `PATH`, toolchain
  and tool-cache directories are granted by default; children get a private `TMPDIR`.
