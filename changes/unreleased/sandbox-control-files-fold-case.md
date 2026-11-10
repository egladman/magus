### Security

- **The sandbox refuses a control file whatever its letter case.** On a
  case-insensitive filesystem such as macOS, a write to `.GIT/hooks/pre-commit`,
  `.ENVRC` or `.Claude/settings.json` passed the binding check, the only layer
  there. Names are now compared with case folded on every platform.
- **The launcher's rule encoding carries any path.** A comma in a granted path
  split its rule, so a path holding `,rwx:/` granted the whole filesystem to the
  confined child. Backslashes and commas in a path are now escaped.
- **An unreadable job store stops a leased run.** When a run claimed a lease and
  the job store could not be read, the sandbox kept the whole workspace write
  grant and only warned (MGS2004). It now refuses to start with an MGS2004 error.
