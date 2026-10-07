### Changed

- **Breaking: `vcs\ref()` returns `str?`.** It is null when no name
  points at the revision: a detached git HEAD, which read as the branch `HEAD`, or jj's
  anonymous working-copy change. Write `vcs\ref() ?? ""` where a string is wanted. It
  still raises when there is no VCS or the backend fails.
