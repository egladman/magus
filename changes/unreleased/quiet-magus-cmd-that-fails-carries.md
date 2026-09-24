### Fixed

- **A quiet `magus\cmd` that fails carries the child's stderr in its error.** The
  Workflows pass `secrets.GITHUB_TOKEN` as `GITHUB_TOKEN`, which `gh` and the github
  queue provider both read, in place of `GH_TOKEN`.
