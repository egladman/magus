### Fixed

- **`magus job fork` refuses a deny path no footprint can grade (MGS3031).** A
  declaration deny on a file with no diff driver, on a pattern, or with nothing after
  the `#` is refused, as the same write path already was.
