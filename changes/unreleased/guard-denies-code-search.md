### Added

- **The guard denies a GitHub code search of this workspace's own repository.**
  `gh search code` or `gh api search/code` naming the repository the checkout's remote
  names is refused and served `magus refs` and `magus query` instead. Other
  repositories, GitHub-wide searches and non-code searches run as typed.
