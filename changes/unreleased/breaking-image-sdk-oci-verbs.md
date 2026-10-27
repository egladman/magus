### Changed

- **Breaking: the `libs/image` and `libs/oci` Go API uses one verb per kind of work.**
  `oci.Client.Manifest` is `GetManifest`, `FetchBlob` is `GetBlob`, and `PutManifest` is
  `PushManifest(ctx, repo, rawManifest, tags...)`. `image.Pull(ctx, client, ref, platform,
  dest)` takes a digest-pinned reference (resolve a tag first) and `image.Push(ctx, client,
  layout, repo, tags...)` returns the pinned `oci.Reference`; `PullOptions`, `PushOptions`,
  `AllowTag` and `Pushed` are gone. `ManifestLayout` is `Manifest`,
  `Layout.Manifest(ctx, platform)` replaces `ResolvePlatform`, `OpenLayout` takes the
  `ref.name` to open, the layer receipt `Added` is `Layer`, the policy constants carry
  their type's name (`ModePolicyFixed`, `SymlinkPolicyKeep`), `Tree.Modes` is
  `Tree.ModePolicy`, `Provenance.Materials` is `Dependencies`, and `image.Verify` and
  `image.ParseDigest` are `oci.VerifyBlob` and `oci.ParseDigest`. `StatusError.Code` is
  `StatusCode` and `Detail` is `Body`, beside the parsed `Errors`.

### Added

- **`image.Platform` carries `OSVersion` and `OSFeatures`, and amd64 takes variants v1 to
  v3**; `GoEnv` sets `GOAMD64` for one. `oci.Client.Referrers` lists a subject's referrers.
