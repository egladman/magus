### Fixed

- **A recursive write path covers a narrower glob beneath it.** A write path
  `docs/**/*.md` covers an output `docs/guides/**/*.md`.
