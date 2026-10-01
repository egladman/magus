### Removed

- **`magus-utils sign`.** The `release-sign` target signs `dist/SHA256SUMS` with
  `crypto\signFile` directly, reading the key from `MAGUS_SIGNING_KEY` as before, so a
  release no longer compiles a Go program to sign one file.
