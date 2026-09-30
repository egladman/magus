### Removed

- **`magus self refresh`, `magus self registry`, the `registry-freshness` doctor check,
  `MAGUS_REGISTRY_URL` and `registry.d` drop-ins are gone.** The signed end-of-life
  registry they read was never published, so `self refresh` always refused. The workflow
  and `hack/toolchain-releases.buzz` that built it are deleted too.
