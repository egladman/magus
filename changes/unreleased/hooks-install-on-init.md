### Changed

- **Only `magus init --vcs git` installs the refresh and drift hooks.** Server start
  refreshes the sections magus already wrote, so they stay current, and installs none
  into a repository that has none: it logs one notice naming `magus init --vcs git`.
