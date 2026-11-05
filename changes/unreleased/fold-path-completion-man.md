### Changed

- **Breaking: `magus path <a> <b>` is `magus explain <a> --to <b>`.** The chain, its
  text and its `-o json` record are unchanged; `path` is no longer a subcommand and
  has no alias. `magus\graph().path` keeps its name.
- **Breaking: `magus completion` and `magus man install` are `magus self completion`
  and `magus self man`.** Update the line in your shell rc that runs `completion`,
  and regenerate a script written to `$fpath` or `~/.config/fish`. Neither old name
  has an alias.
