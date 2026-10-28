### Added

- **magus indexes Buzz and shell scripts itself, one file at a time.** A spell's symbol
  indexer declaring `format = SymbolFormat.buzzModule` binds a project's Buzz in process,
  and `SymbolFormat.shellScript` indexes its `*.sh` and `*.bash` files with mvdan.cc/sh:
  functions, the variables a script gives global scope, function locals, and the files a
  script sources. An edit re-indexes the file it touched, and the files that import or
  source it only when a name they bind there was added or removed. A file that does not
  parse is named in a warning on every index run, rather than left out with no word.
