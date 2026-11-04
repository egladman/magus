### Changed

- **The checkpoint advice stays off read-only rows.** A scout that asked git for the current revision was told to run the checkpoint verb and read that as a required write. A read-only lease has nothing to park, so the advisory no longer reaches it; a writer lease still gets it.
