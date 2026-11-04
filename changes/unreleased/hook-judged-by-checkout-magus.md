### Changed

- **A hook call is judged by the `./magus` of the checkout it lands in.** When that binary
  is not the session's own, the session's binary hands the call on.
