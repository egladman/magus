### Added

- **magus indexes Buzz and shell scripts itself, one file at a time.**
  `SymbolFormat.buzzModule` binds a project's Buzz in process, and
  `SymbolFormat.shellScript` indexes `*.sh` and `*.bash` with mvdan.cc/sh. An edit
  re-indexes the file it touched, and its importers only when a name they bind changed.
  A file that does not parse is named in a warning.
