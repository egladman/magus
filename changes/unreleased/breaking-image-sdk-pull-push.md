### Changed

- **Breaking: `image.Pull` takes a digest-pinned reference and `image.Push` returns one.**
  Resolve a tag before `Pull(ctx, client, ref, platform, dest)`. `Push(ctx, client, layout,
  repo, tags...)` returns the pinned `oci.Reference`. `PullOptions`, `PushOptions`,
  `AllowTag` and `Pushed` are gone.
