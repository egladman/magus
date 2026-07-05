---
title: Run magus in Docker
description: Pull and run the official magus container images from GHCR - a multi-arch static image and a glibc variant - mounting your workspace, plus how to verify the cosign signature.
tags: [docker, container, image, ghcr, cosign, install]
---

# Run magus in Docker

Official images are published to the GitHub Container Registry at
**`ghcr.io/egladman/magus`**. They are a drop-in alternative to installing the
binary - handy for CI runners and throwaway environments.

Each image runs magus as its entrypoint against `/workspace`, as a non-root user.
Mount your repository there and pass a magus command:

```sh
docker run --rm -v "$PWD":/workspace ghcr.io/egladman/magus:latest ls
docker run --rm -v "$PWD":/workspace ghcr.io/egladman/magus:latest run ci
```

The default command is `ls` (list projects and targets), so a bare
`docker run --rm -v "$PWD":/workspace ghcr.io/egladman/magus:latest` is a quick smoke
test.

## Variants

| Image | Base | Platforms | Notes |
|-------|------|-----------|-------|
| `ghcr.io/egladman/magus:latest` | distroless/static | linux/amd64, linux/arm64 | Pure-Go static build. The default; use this unless you have a reason not to. |
| `ghcr.io/egladman/magus:latest-cgo` | distroless/cc (glibc) | linux/amd64 | glibc build that bundles `inotify-tools`, so `magus watch` / `fs.watch` work inside the container. |

## Tags

- `latest` / `latest-cgo` - the most recent release.
- `<version>` / `<version>-cgo` - a specific release, e.g. `v0.4.2` or `v0.4.2-cgo`. Pin these in CI so a run is reproducible.

```sh
docker pull ghcr.io/egladman/magus:v0.4.2
```

## Verify the signature

Every pushed image digest is signed with [cosign](https://github.com/sigstore/cosign)
keyless (Sigstore OIDC) at release time - no long-lived key. Verify that an image was
built by this project's release workflow:

```sh
cosign verify ghcr.io/egladman/magus:latest \
  --certificate-identity-regexp '^https://github.com/egladman/magus/.github/workflows/cd.yaml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

A missing or mismatched signature means the image is not an official build - do not
run it.

## Next steps

- New to magus? Start with [Targets](../targets.md) and [Spells](../spells.md).
- Prefer a native binary? See the [install guides](../download.md#install).
