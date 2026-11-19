### Changed

- **`libs/clikit` runs a command line, not only renders one.** `command.Main` parses a
  declared `command.Command` tree, dispatches to its children, prints help from the
  declaration and exits through `exit.Code`. A `Flag` stores into its `Target`, a `Command`
  runs its `Run`, and `command.WithVersion` adds `--version`.
- **`command.Main` cancels its context on the first SIGINT or SIGTERM**, and a second one
  kills the command.
- **The `clikitmain` analyzer reports a main package that parses its command line by
  hand**: a flag defined or parsed through package `flag`, or a read of `os.Args`. Each
  module's `analyzers.yaml` holds today's count per file under `clikitmain`, which only
  shrinks.
- **Breaking: `magus-utils`, the docs generators, `magus-termcast`, `langservice-manifest`
  and `buzz` are built on `libs/clikit`.** `-h` prints the command's help to stdout and
  exits 0. A misused flag or command prints one line naming the command's `-h` and exits 2.
  Every bool flag also takes `--no-<name>`.
- **Breaking: `magus-utils cut` and `release-index` refuse a flag they do not declare**
  instead of ignoring it, and `release-index -no-sign` is the negation of `--sign`.
- **Breaking: more misuse exits 2 rather than 1**: a `usage:` error from a `magus-utils`
  subcommand, an unknown `magus-manpage -format`, and a `buzz` option error.
