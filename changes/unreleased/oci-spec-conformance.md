### Fixed

- **`docker.io` references reach Docker Hub.** `docker.io` and `index.docker.io` requests
  go to `registry-1.docker.io`, the host that serves the API; a one-segment Docker Hub
  name such as `docker.io/alpine` is refused with its `docker.io/library/alpine` spelling.
- **`image\pull` and `image\push` copy any well-formed image.** Docker schema 2 manifests,
  indexes and layers are kept as served, a non-distributable layer is described and not
  transferred, and an assemble over a Docker base writes it back as OCI.
- **Referrers are found on registries without the Referrers API.** A pushed SBOM or
  provenance referrer is also listed in the subject's `sha256-<hex>` tag index when the
  registry answers without `OCI-Subject`, so `cosign`, `oras` and `trivy` see it.
- **A layout from another tool opens.** An image is the `index.json` entry with no
  subject, chosen by `org.opencontainers.image.ref.name` when there are several; referrers
  are the entries whose subject it is, and entries of an unknown media type are passed over.
- **`base.name` and `base.digest` name the same manifest** when the base was pulled by an
  index digest.
- **An assemble over a base keeps the config members magus does not model**, such as a
  `Healthcheck` or `Shell`.
- **A placed path named `.wh.*` is refused**: in a layer it is a whiteout that deletes the
  base's file.
- **Registry errors name their codes.** The `{"errors": [...]}` body is parsed, a 403 is
  `oci.ErrDenied` rather than `ErrUnauthorized`, a 429 with a `Retry-After` of a minute or
  less is retried and a longer one fails as `oci.ErrTooManyRequests`, and a `Warning: 299`
  header is logged.
