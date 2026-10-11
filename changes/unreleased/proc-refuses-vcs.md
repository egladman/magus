### Changed

- **Breaking: `proc\exec` and `os.execute` refuse git, hg, sl and jj.** A magusfile
  that runs one by name, by path, behind a wrapper or in a `proc\shell` line now raises
  with the `vcs\cmd` call to make instead, so every commit a script makes passes the
  lease rules.
