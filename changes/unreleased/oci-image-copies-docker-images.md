### Fixed

- **`image\pull` and `image\push` copy any well-formed image.** Docker schema 2 manifests,
  indexes and layers are kept as served, a non-distributable layer is described and not
  transferred, and an assemble over a Docker base writes it back as OCI.
