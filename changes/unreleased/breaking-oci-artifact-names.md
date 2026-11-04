### Changed

- **Breaking: `libs/oci` names an artifact once.** `oci.Content` is `oci.Artifact`, whose
  `Blobs` replace `Layers`; `Client.Artifact` is `Client.GetArtifact`, `ErrLayerMiss` is
  `ErrNoBlob`, and `Client.Push` is `Client.PushArtifact(ctx, repo, a, tags...)`, which
  refuses a `repo` carrying a tag or digest and pushes the manifest by digest, then
  under each tag.
