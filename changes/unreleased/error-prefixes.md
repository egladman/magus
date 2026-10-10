### Changed

- **An error names its origin once.** Errors no longer open with another package's
  name (`magus: unknown project` is `unknown project`, and a job record that fails
  validation reads `invalid job: ...`), and a wrap no longer repeats its own package's
  prefix. The `errmsg` linter enforces both with `error-origin` and `error-stutter`.
