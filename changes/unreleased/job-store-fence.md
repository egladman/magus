### Changed

- **Breaking: only a magus built on the base migrates the shared job store.** A binary
  not built on a clean commit the base contains is refused before anything is read or
  written: rebuild it from the base, or run it under its own `XDG_STATE_HOME`. This
  replaces the refusal from a live job's checkout.
- **The job store names its writer.** Every write and migration stamps the writing
  magus's version and commit in the header, and a refusal of a newer or off-base store
  names the magus that wrote it last.
- **A job store whose header was lost is refused, not replayed.** A headerless file whose
  rows are enveloped is refused as damaged, naming the command that moves it aside.
