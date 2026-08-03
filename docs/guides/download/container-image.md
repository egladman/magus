---
title: Run magus from a container image
description: Pull and run the official magus OCI images from GHCR with Docker, Podman, or any OCI runtime, mount your workspace, extract the binary without running a container, and verify the cosign signature.
tags:
  [
    container,
    container image,
    image,
    oci,
    docker,
    podman,
    nerdctl,
    ghcr,
    registry,
    cosign,
    install,
  ]
aliases: [guides/download/docker]
---

# Run magus from a container image

Official images are published to the GitHub Container Registry at
**`ghcr.io/egladman/magus`**. They are a drop-in alternative to installing the
binary, which is convenient on CI runners and in throwaway environments.

They are ordinary [OCI](https://opencontainers.org/) images with nothing
Docker-specific about them. The examples below use `docker` because it is the
most familiar, and every one of them works unchanged with `podman`, `nerdctl`, or
any other OCI runtime. Browse the published tags and digests on the
[package page](https://github.com/egladman/magus/pkgs/container/magus).

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
- **Buzz FFI (`zdef()`) is unavailable in every static build.** That means this image and
  the unsuffixed release archives, so it applies equally to a binary you extract with
  `docker cp` below. FFI opens a shared library at runtime, which is what made those builds
  need a dynamic loader; they are compiled with `-tags noffi`, and `zdef()` reports FFI as
  unsupported rather than failing at the call. Keeping it would have cost the static
  property itself: the loader it pulls in is exactly what `distroless/static`, a scratch
  image, and a musl host do not provide. Use the cgo image, or a `-cgo` archive, if a
  magusfile calls `zdef()`.

## Tags

- `latest` / `latest-cgo` follows the most recent release.
- `<version>` / `<version>-cgo` pins one release, for example `__MAGUS_VERSION__` or
  `__MAGUS_VERSION__-cgo`. Pin a version in CI so a run stays reproducible.

```sh
docker pull ghcr.io/egladman/magus:__MAGUS_VERSION__
```

## Install the binary without running a container

The static image is a single static binary on an empty base, so the image doubles as
a distribution channel: copy the binary out and run it on the host, no container
runtime involved at run time. This is handy on a machine that already has a runtime
but no install script, and it is how several projects distribute cross-platform
tools.

```sh
id=$(docker create ghcr.io/egladman/magus:__MAGUS_VERSION__)
docker cp "$id":/magus ./magus
docker rm "$id"
./magus version
```

`podman` works the same way with `podman create` / `podman cp`. Pin a version rather
than `latest`, so what you extract is reproducible.

This only works with the static image. The binary in the `-cgo` image is linked
against that image's glibc and will not run on an arbitrary host. What you extract is
the same static build the unsuffixed release archives ship, so it has no Buzz FFI
either (see Variants).

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
