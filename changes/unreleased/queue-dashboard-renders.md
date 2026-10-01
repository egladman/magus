### Fixed

- **The queue dashboard renders and edits its issue.** It read 100 merged pull requests
  with their checks, which timed out GitHub every run; it reads 40. `--issue` is now
  required, and `--preview` writes only the file, so an unset issue variable fails the job
  instead of passing green with no issue edited.
