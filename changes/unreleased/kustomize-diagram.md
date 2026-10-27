### Added

- **A kustomize base runs the daemon on Kubernetes for development.** `deploy/kustomize/base`
  runs `magus server start --foreground` under the restricted pod security profile: non-root,
  read-only root filesystem, no capabilities, seccomp `RuntimeDefault`, no service account
  token, a claim for state and a default-deny NetworkPolicy. `deploy/kustomize/overlays/dev`
  puts it in `magus-dev`. `magus run lint libs/kustomize` validates both builds with
  kubeconform against vendored Kubernetes v1.35.0 schemas.
  [Guide](https://eli.gladman.cc/magus/guides/kustomize-diagram/).
- **`libs/kustomize` reads a kustomization in Buzz and draws it.** `build` follows
  `kustomize build` without the binary and raises on a field or patch it cannot follow;
  `figureOf` draws the objects through magus/figure. The docs figure
  `kustomize-architecture` is read from the dev overlay.
