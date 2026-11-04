### Changed

- **Breaking: the `image` module's Buzz names.** A descriptor's `mediaType` and
  `artifactType` are `str`. `image\platform` is `image\parsePlatform`,
  `image\writeSbom` is `image\writeSBOM`, `Tree.modes` is `Tree.modePolicy`,
  `Provenance.materials` is `dependencies`, and `image\Added` is `image\AddedLayer`.
  `image\open` takes an optional `name`.
