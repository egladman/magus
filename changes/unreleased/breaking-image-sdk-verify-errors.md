### Changed

- **Breaking: `image.Verify` and `image.ParseDigest` are `oci.VerifyBlob` and
  `oci.ParseDigest`.** `StatusError.Code` is `StatusCode` and `Detail` is `Body`, beside the
  parsed `Errors`.
