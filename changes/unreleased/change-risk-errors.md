### Fixed

- **Change risk reports each read error as itself.** A coverage block repeated across
  test binaries is merged, not double-counted. A version control that fails to resolve,
  an unreadable profile, or an unparsable symbol end line fails with its own error,
  never as disabled, missing or one line. A file no spell claims is a gap, not a crash.
