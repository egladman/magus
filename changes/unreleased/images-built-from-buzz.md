### Changed

- **Both container images are assembled by magus, not Docker.** `image-build` builds the
  static and dynamic images from `hack/magusfile/image.buzz` with the `image` module, on
  digest-pinned distroless bases, and attaches an SBOM and provenance. A local build loads
  the image and runs `magus version` in it. `Dockerfile` and `Dockerfile.static` are gone.
