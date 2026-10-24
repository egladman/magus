### Changed

- **The design principles state "No silent defaults".** It replaces the "Misconfiguration is an error"
  entry: a default that changes what happens without saying so is a bug, a fallback says that
  it fell back, and an absent capability fails loudly rather than answering empty.
