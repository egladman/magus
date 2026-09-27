### Fixed

- **`self update` refusals hand back flags that exist.** Downgrade, reinstall and
  unversioned-build errors named `--uf.Force` / `--uf.Yes`, which are struct fields,
  not flags; they now say `--force` and `--yes`/`-y`. A declined install exits nonzero
  instead of 0. The confirmation prompt names the host the release index came from.
