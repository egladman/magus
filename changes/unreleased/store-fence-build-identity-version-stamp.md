### Changed

- **Breaking: the version stamp moved to
  `-X github.com/egladman/magus/internal/buildinfo.version`.** A build that still passes
  `-X main.version` reports version `unknown`, and the job store fence then judges it by
  Go's VCS stamp.
