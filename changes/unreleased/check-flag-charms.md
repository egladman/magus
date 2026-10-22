### Fixed

- **`job fork --check` accepts `--no-default-charms`.** `--check 'lint . --no-default-charms'`
  was refused with a pointer to a JSON record, so a fork from the command line could not name
  a check the guard then demanded. The flag now sets `no_default_charms` on the check; every
  other magus flag before `--` is still refused.
