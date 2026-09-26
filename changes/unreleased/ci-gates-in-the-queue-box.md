### Added

- **`magus queue gate` runs a command in the box `queue validate` gives a candidate.** It
  checks HEAD out with a private home and temporary directory under the hook sandbox.
  `--cache` keeps the local tier outside the box; `--env` passes named variables to the
  command's own magus. CI's shards now gate through it.
