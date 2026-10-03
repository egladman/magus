### Changed

- **`magus buzz -C <dir>` changes to that directory before anything else, as `go -C`
  does.** It applies in every mode, so a script path and its imports resolve from
  `<dir>` wherever the command runs; a missing directory is an error naming it. It used
  to aim only the REPL's import resolution.
