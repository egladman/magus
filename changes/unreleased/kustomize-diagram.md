### Added

- **A kustomize base runs the daemon on Kubernetes for development.** `deploy/kustomize/base`
  runs `magus server start --foreground` under the restricted pod security profile with a
  default-deny NetworkPolicy, and `overlays/dev` puts it in `magus-dev`. `magus run lint
  libs/kustomize` validates both builds with kubeconform.
  [Guide](https://eli.gladman.cc/magus/guides/kustomize-diagram/).
