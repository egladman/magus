### Changed

- **Breaking: layout and policy names in `libs/image` follow their types.** `ManifestLayout`
  is `Manifest`, `Layout.Manifest(ctx, platform)` replaces `ResolvePlatform`, `OpenLayout`
  takes the `ref.name` to open, and the receipt `Added` is `Layer`. The constants are
  `ModePolicyFixed` and `SymlinkPolicyKeep`, `Tree.Modes` is `Tree.ModePolicy`, and
  `Provenance.Materials` is `Dependencies`.
