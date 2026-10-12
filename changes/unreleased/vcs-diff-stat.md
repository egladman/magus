### Added

- **`vcs\diffStat` counts a change's added and deleted lines on every backend.** It returns one
  `{path, added, deleted, binary}` per file for the revision past its merge base, on git,
  Mercurial, Sapling and Jujutsu. A script that passed git's `diff --numstat` through
  `vcs\cmd` failed quietly on the other three.
