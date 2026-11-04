### Changed

- **Breaking: more `image` module renames.** `image\platforms` is `image\parsePlatforms`,
  `Provenance.builderId` is `builderID`, `image\AddedLayer.layer` is `descriptor`, and
  `Provenance.dependencies` holds `image\Dependency`, SLSA v1's resolved dependencies.
  `image\MediaType` keeps the types magus writes as named values.
