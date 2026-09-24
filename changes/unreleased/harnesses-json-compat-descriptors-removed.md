### Removed

- **Breaking: `harnesses/*.json` compat descriptors are removed.** All four shipped hosts are
  Buzz spells under `spells/harness/`, wired with `magus\harness.provider(...)`. JSON
  descriptors under `harnesses/`, `.magus/harnesses/` and `$XDG_CONFIG_HOME/magus/harnesses`
  are no longer read, and `--id` now resolves only a wired spell. Adapt a host by
  forking its spell's import path instead.
