### Fixed

- **Referrers are found on registries without the Referrers API.** A pushed SBOM or
  provenance referrer is also listed in the subject's `sha256-<hex>` tag index when the
  registry answers without `OCI-Subject`, so `cosign`, `oras` and `trivy` see it.
