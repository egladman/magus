### Changed

- **Breaking: `magus completion` and `magus man install` are `magus self completion`
  and `magus self man`.** Update the line in your shell rc that runs `completion`,
  and regenerate a script written to `$fpath` or `~/.config/fish`. Neither old name
  has an alias.
