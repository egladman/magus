### Fixed

- **A brief is refused exactly when its worker's run would be.** The spawn grader and
  `worker-check-only` share one verdict, so a brief teaching a slice of the check, an op
  the check composes or a dependency install is no longer refused at spawn.
- **A rewrite the guard cannot check is refused, not passed.** When the workspace does not
  load, a leased run under the rw charm is refused with the load error and the same run
  without the charm.
- **A run naming no project is judged in the project holding the caller's directory**, as
  `magus run` selects it, rather than as the root project.
- **The run offered in place of a rewrite keeps the arguments after `--`** and the other
  charms, and adds `--no-default-charms` only when the default charms carry rw.
- **A recursive write path covers a narrower glob beneath it.** A write path
  `docs/**/*.md` covers an output `docs/guides/**/*.md`.
- **One leased run judgment opens the workspace once**, not up to three times inside the
  hook's deadline.
