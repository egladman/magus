### Changed

- **`proc\exec` refuses git, hg, sl and jj.** Run by name, by path, behind a wrapper
  or inside a `proc\shell` line, version control now raises with the `vcs\cmd` call to
  make instead, so every commit a script makes passes the lease rules.
