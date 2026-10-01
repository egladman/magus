### Removed

- **`magus-utils verify`.** `release-sign` checks the signature it wrote with
  `crypto\verify` against the active key in `internal/selfupdate/release-keys.json`, the
  keyring of the tree being released, so a mis-set signing key fails the release rather
  than every client's self-update.
