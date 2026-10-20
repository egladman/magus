### Added

- **`magus diff` reports a symbol past its conformance threshold.** With `knowledge.conformance.ratchet` set, a symbol the change adds, renames or re-signs that measures past the committed value, or the configured anchor where none is committed, is advice on that symbol, and so is one that passes only because the change raised a committed value or listed it as known.
