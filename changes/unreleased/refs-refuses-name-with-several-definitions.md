### Fixed

- **`refs` refuses a name several definitions share.** It used to answer for whichever one
  ranked first. It now exits 2 and lists `magus refs '<id>'` for each candidate.
