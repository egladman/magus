### Changed

- **A mise shim's version probe reads each mise state directory once.** Trusted
  configs, ignored configs and installs key on each directory's identity and its
  entries' names and types, with no readlink per entry. A directory changed in
  the last three seconds keys nothing: the probe runs uncached and says so.
