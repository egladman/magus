### Fixed

- **The kustomize library tests find their testdata from any importer.** They read
  testdata and the dev overlay against the working directory, so the six that docs runs
  failed there; they now resolve against the repository root.
