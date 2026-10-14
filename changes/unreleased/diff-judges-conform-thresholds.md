### Added

- **`magus diff` reports a symbol past its conform threshold.** With
  `knowledge.conform.ratchet` set, a symbol the change adds, renames or re-signs that measures
  past the committed value, or the configured anchor where none is committed, is advice on that
  symbol, and so is one that passes only because the change raised a committed value or listed
  it as known. A ratchet record that is missing or invalid fails the conformance checks with
  the reason; a workspace with no ratchet configured sees nothing new.

### Changed

- **`magus\precedents` reads `knowledge.conform`.** Its gates decide which rows are
  established, and a dep-fanout row's ceiling is the layer's committed or configured value; a
  layer with neither gets no fan-out row.
