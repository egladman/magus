### Changed

- **Breaking: `libs/oci` names an artifact once.** `oci.Content` is `oci.Artifact`, with
  `Blobs []oci.Blob` (`Title`, `MediaType`, `Data`) in place of `Layers []oci.Layer`, and
  its `Manifest` returns an `oci.RawManifest`. The pull handle is `oci.ArtifactReader`,
  `Client.Artifact` is `Client.GetArtifact`, and `ErrLayerMiss` is `ErrNoBlob`.
  `Client.Push(ctx, ref, content, moreTags...)` is `Client.PushArtifact(ctx, repo, a,
  tags...)`: `repo` names the repository alone, a reference with a tag or digest is
  refused, and the manifest goes up by digest and then under each tag.

### Added

- `oci.Client.MountBlob` asks the registry to mount a blob from another repository on it,
  and uploads the blob when the registry declines. `image.Push` mounts the blobs of an
  image whose `base.name` is another repository on the push's registry.
- A blob over `oci.Client.ChunkSize` (64 MiB by default, raised to a registry's
  `OCI-Chunk-Min-Length`) uploads in chunks, and a chunk the registry answers with 416 is
  sent again from the offset it holds.
- `image.AssembleIndex` keeps each input's referrers, such as a per-platform SBOM, in the
  index layout, and `image.Push` sends them.
