### Fixed

- **Figures draw again.** `magus/figure` imports `result`, and `magus job graph -o svg`,
  the console's diagrams and the playground did not declare it.
- **A start service that cannot start says why.** When its start and its readiness probe
  both fail, the error carries the probe's stderr.
- **`hack/remote/on-linux.buzz` names a wedged podman machine.** A machine listed as
  running that `podman info` cannot reach is refused with the probe's words and the
  restart that clears it.
- **`hack/dev/bootstrap-checkout.buzz` runs `cmd/magus/main.go`'s generators** after a
  failed build, beside the boundary generators.
