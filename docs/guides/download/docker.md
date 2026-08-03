---
title: Run magus in Docker
description: Pull and run the official magus container images from GHCR, mount your workspace into them, and verify the cosign signature. Covers the multi-arch static image and the glibc variant.
tags: [docker, container, image, ghcr, cosign, install]
---

# Run magus in Docker

Official images are published to the GitHub Container Registry at
**`ghcr.io/egladman/magus`**. They are a drop-in alternative to installing the
binary, which is convenient on CI runners and in throwaway environments.

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

| Image                               | Base                  | Platforms                | Notes                                                                                              |
| ----------------------------------- | --------------------- | ------------------------ | -------------------------------------------------------------------------------------------------- |
| `ghcr.io/egladman/magus:latest`     | distroless/static     | linux/amd64, linux/arm64 | Fully static, no libc. The default; use this unless you need something below.                      |
| `ghcr.io/egladman/magus:latest-cgo` | distroless/cc (glibc) | linux/amd64              | glibc build that bundles `inotify-tools`, so `magus watch` / `fs\watch` work inside the container. |

Use `latest` unless you know you need the other one. Two things differ beyond the base
image, and both follow from the static image carrying no shared libraries at all:

- **`magus watch` / `fs\watch`** need `inotify-tools`, which only the cgo image ships.
- **Buzz FFI (`zdef()`) is unavailable in the static image.** FFI opens a shared library
  at runtime, and the static image has none to open, so it is compiled out (`-tags noffi`)
  and `zdef()` reports FFI as unsupported rather than failing at the call. Keeping it in
  would have cost the static property for a capability nothing in that image can use: the
  loader it pulls in is exactly what `distroless/static` does not provide. Use the cgo
  image if a magusfile calls `zdef()`.

## Tags

- `latest` / `latest-cgo` follows the most recent release.
- `<version>` / `<version>-cgo` pins one release, for example `__MAGUS_VERSION__` or
  `__MAGUS_VERSION__-cgo`. Pin a version in CI so a run stays reproducible.

```sh
docker pull ghcr.io/egladman/magus:__MAGUS_VERSION__
```

## Verify the signature

Every pushed image digest is signed with [cosign](https://github.com/sigstore/cosign)
at release time, keyless through Sigstore OIDC, so there is no long-lived key to
leak. To confirm an image came from this project's release workflow:

```sh
cosign verify ghcr.io/egladman/magus:latest \
  --certificate-identity-regexp '^https://github.com/egladman/magus/.github/workflows/cd.yaml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

A missing or mismatched signature means the image is not an official build. Do not
run it.

## Next steps

- New to magus? Start with [Targets](../../concepts/targets.md) and [Spells](../../concepts/spells.md).
- Prefer a native binary? See the [install guides](../download.md#install).
