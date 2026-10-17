### Changed

- **Embedding search scores every document exactly, and the index format is 2.** The
  sign-code first stage kept 77% of the exact top 100 at its default depth. An exact
  scan of 100k documents of 256 dims, split across the cores, takes 1.5 ms on an
  Apple M5, and its hits are the same for any core count. `SearchOptions.Rescore` and
  `Vector.Code` are gone, and the index no longer stores sign codes. An index written
  at format 1 is refused; rebuild it.
