### Changed

- **Breaking: `oci.Client` verbs are `GetManifest`, `GetBlob` and `PushManifest`.**
  `Manifest` is `GetManifest`, `FetchBlob` is `GetBlob`, and `PutManifest` is
  `PushManifest(ctx, repo, rawManifest, tags...)`.
