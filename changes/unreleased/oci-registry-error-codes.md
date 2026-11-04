### Fixed

- **Registry errors name their codes.** The `{"errors": [...]}` body is parsed, a 403 is
  `oci.ErrDenied` rather than `ErrUnauthorized`, a 429 with a `Retry-After` of a minute or
  less is retried and a longer one fails as `oci.ErrTooManyRequests`, and a `Warning: 299`
  header is logged.
