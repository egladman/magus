### Changed

- **Breaking: `magus path <a> <b>` is `magus explain <a> --to <b>`.** The chain, its
  text and its `-o json` record are unchanged; `path` is no longer a subcommand and
  has no alias. `magus\graph().path` keeps its name.
