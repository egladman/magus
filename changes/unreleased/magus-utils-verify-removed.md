### Removed

- **`magus-utils verify`.** `release-sign` checks the signature it wrote with
  `crypto\verify` against the new `crypto\releasePublicKey`, the active release key the
  running magus embeds, so a mis-set signing key still fails the release rather than
  every client's self-update.
