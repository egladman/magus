### Changed

- **The agent guard fails closed when it cannot judge.** With no magus found, or one that
  cannot answer, the shipped hooks refuse every call that may change state and let a read
  through. Each refusal tells the person through the host's notice channel how to install
  or build magus. Cursor and OpenCode refuse reads too.
