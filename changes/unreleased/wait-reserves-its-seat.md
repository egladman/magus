### Changed

- **`magus status --wait | magus run ...` keeps the seat it waited for.** The broker holds
  the seat the wait sized until the reading run's first claim takes it, so no other run
  takes it first. It lapses when the reader exits or after 30 seconds without a claim. A
  bare run still refuses at once.
