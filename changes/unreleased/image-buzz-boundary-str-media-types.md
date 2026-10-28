### Changed

- **Breaking: the `image` module's Buzz names.** A descriptor's `mediaType` and
  `artifactType` are `str`, so a base or referrer of any type reads back;
  `image\MediaType` keeps the types magus writes as named values. `image\platform` and
  `image\platforms` are `image\parsePlatform` and `image\parsePlatforms`,
  `image\writeSbom` is `image\writeSBOM`, and `image\open` takes an optional `name`, the
  `org.opencontainers.image.ref.name` of the image to open. `Tree.modes` is
  `Tree.modePolicy`, `Provenance.builderId` is `builderID`, `Provenance.materials` is
  `dependencies` (of `image\Dependency`, SLSA v1's resolved dependencies), and
  `image\Added` is `image\AddedLayer`, whose `layer` is `descriptor`.

### Added

- `image\Platform` carries `osVersion` and `osFeatures`, and `image\Variant` names the
  amd64 variants `v1`, `v2` and `v3`.
