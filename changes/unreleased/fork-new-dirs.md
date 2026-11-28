### Added

- **A job record names the directories its forker approved creating in `creates`.** Each
  entry is one workspace-relative directory. A pattern, the root or a path outside the
  workspace is refused, and so is an entry that already exists. A child creates only
  under its parent's entries.
- **magus measures the directories a fork's write paths would create.** A directory with
  no `dir:` node and nothing on disk is new, listed with the directories sharing a word
  with its name. `magus job fork`, `magus describe job` and
  `magus\workspace().newDirs(writes)` report them. An unread graph says unknown.
- **This repository refuses a fork that creates a package no one picked.** A new directory
  under `internal/`, `libs/`, `cmd/`, `std/` or `types/` missing from `creates` is denied.
  One under `docs/` or `hack/`, or an unknown measure, is reported. It cannot tell an
  owner's name from one a forker wrote into `creates`, nor see a new term in an existing
  directory.
