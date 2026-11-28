### Added

- **A job record names the directories its forker approved creating in `creates`.** Each
  entry is one workspace-relative directory. A pattern, the root or a path outside the
  workspace is refused, and so is an entry that already exists. A child creates only
  under its parent's entries. `fork`, `apply` and `putJob` all carry it.
- **magus measures the directories a fork's write paths would create.** A directory with
  no `dir:` node in the graph and nothing on disk is new, listed with the graph's
  directories whose last segment shares a word with it. `magus job fork` prints each as a
  note, `magus describe job` lists them, and `magus\workspace().newDirs(writes)` hands
  them to policy. An unread graph says unknown, never none.
- **This repository refuses a fork that creates a package no one picked.** A new directory
  under `internal/`, `libs/`, `cmd/`, `std/` or `types/` that the record does not list in
  `creates` is denied, naming its neighbours and the repair. One under `docs/` or `hack/`
  is reported, and so is an unknown measure. The check cannot tell a directory the owner
  named from one a forker wrote into `creates` itself, and it does not see a new term
  added inside an existing directory.
