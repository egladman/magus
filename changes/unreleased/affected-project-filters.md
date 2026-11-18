### Changed

- **Breaking: `magus affected <target>` refuses a project operand.** It runs every project the
  diff reaches, so a named project, `.` and `/` included, is refused with a usage error that
  names `magus run <target> <projects>`, the verb that picks projects. Before, a normal run
  dropped the operands without a word.
