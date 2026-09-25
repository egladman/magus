### Added

- **`magus\skills(opts)` returns the skills a workspace offers an agent.** Each
  entry carries its name, description, source (`shipped` or `local`), form, the
  body an agent loads, and whether the installed copy is current. `opts.name`
  selects one skill and `opts.form` picks `short` or `full`, so a script can hand
  a worker its skills from magus rather than from pasted files.
