### Fixed

- **Installs of magus-managed git, hg and Sapling sections are atomic.** They are
  serialized per repository and leave a hook executable. A torn section marker is an error.
