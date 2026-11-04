### Added

- **`libs/kustomize` reads a kustomization in Buzz and draws it.** `build` follows
  `kustomize build` without the binary and raises on a field or patch it cannot follow;
  `figureOf` draws the objects through magus/figure. The docs figure
  `kustomize-architecture` is read from the dev overlay.
