---
title: Run magus from a container image
order: 4
description: Pull and run the official magus OCI images from GHCR or Docker Hub with Docker, Podman, or any OCI runtime, mount your workspace, extract the binary without running a container, read the SBOM, and verify the cosign signature.
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
    docker hub,
    registry,
    cosign,
    sbom,
    provenance,
    install,
  ]
aliases: [guides/download/docker]
---

# Run magus from a container image

Official images are published to two registries:

- **`ghcr.io/egladman/magus`** on the GitHub Container Registry
- **`docker.io/egladman/magus`** on Docker Hub

Both receive the same tags from the same build, so a given tag is the same image
digest in either place. Pick whichever your environment already authenticates
against; GHCR is the primary, and the rest of this page names it for brevity.
Either way, the images are a drop-in alternative to installing the binary, which
is convenient on CI runners and in throwaway environments.

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

### Snapshot images (per commit on main)

Every merge to `main` also publishes a static image tagged with that commit's short
hash, so you can run an exact commit without waiting for a release:

```sh
docker run --rm -v "$PWD":/workspace ghcr.io/egladman/magus:a1b2c3d ls
```

These are **not releases**. They are GHCR-only (never Docker Hub), static-only (no
`-cgo` variant), have no moving tag to follow, are not pruned on any schedule, and
carry no compatibility promise. They are signed, but by the CI workflow rather than
the release workflow, so the verify command below deliberately rejects them - see
[Verify the signature](#verify-the-signature).

## Build the image yourself

```sh
magus run image-build
```

A local build loads a **single architecture** into your docker image store, and says
which one in the build log. That is a constraint rather than a preference: `docker
buildx build --load` cannot load a multi-platform result into the image store, so a
local build has exactly one architecture no matter what. It defaults to your host's,
so an Apple Silicon machine builds and runs `linux/arm64` natively.

| Command | Builds |
| --- | --- |
| `magus run image-build` | cgo variant, host architecture |
| `magus run image-build:static` | static variant, host architecture |
| `magus run image-build:static,arm64` | static variant, forced `linux/arm64` |
| `magus run image-build:amd64` | cgo variant, forced `linux/amd64` |

The `amd64` and `arm64` charms exist for reproducing a failure that only happens on
the other architecture. They go through QEMU emulation, so expect them to be slow -
they are a debugging tool, not a second default.

Multi-architecture images are built only when publishing, where the result is pushed
to a registry rather than loaded locally.

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

**Use cosign v3 or later.** Signatures are written in the Sigstore bundle format that
v3 made the default. A v3 client verifies both formats, but a v2 client cannot read a
v3 signature and reports the image as unverified - which looks exactly like a bad
signature. Check with `cosign version` before concluding anything from a failure.

A missing or mismatched signature means the image is not an official build. Do not
run it.

A signature lives in the registry beside the image it covers, so it is pushed to
each registry separately even though the digest is identical. Verify the reference
you actually pulled - swap `ghcr.io` for `docker.io` above if that is where the
image came from.

The identity regexp pins the release workflow specifically, which is what makes the
command reject a [snapshot image](#snapshot-images-per-commit-on-main):
those are signed too, but under `ci.yaml`, so they fail this check by design rather
than by accident. To verify one on purpose, name that workflow instead:

```sh
cosign verify ghcr.io/egladman/magus:a1b2c3d \
  --certificate-identity-regexp '^https://github.com/egladman/magus/.github/workflows/ci.yaml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Read the SBOM

Every published image carries an [SPDX](https://spdx.dev/) software bill of
materials and a max-detail [SLSA](https://slsa.dev/) provenance statement, both
attached as in-toto attestations inside the image index. They are generated by
BuildKit during the build itself, one per platform, so what they describe is the
filesystem that actually shipped rather than a later re-scan of it.

Because they are part of the index, no extra registry lookup is needed:

```sh
docker buildx imagetools inspect ghcr.io/egladman/magus:latest --format '{{ json .SBOM }}'
docker buildx imagetools inspect ghcr.io/egladman/magus:latest --format '{{ json .Provenance }}'
```

On a multi-platform tag, pick a platform to avoid dumping every architecture at once:

```sh
docker buildx imagetools inspect ghcr.io/egladman/magus:latest \
  --format '{{ json (index .SBOM "linux/arm64").SPDX }}'
```

The SPDX document is ordinary JSON, so any SBOM tool reads it. To feed it to a
vulnerability scanner:

```sh
docker buildx imagetools inspect ghcr.io/egladman/magus:latest \
  --format '{{ json (index .SBOM "linux/amd64").SPDX }}' > magus.spdx.json
grype sbom:magus.spdx.json
```

The static image is a single Go binary on an empty base, so expect its SBOM to be
short: the Go module graph and essentially nothing else. The `-cgo` image also lists
its distroless/cc glibc layer and `inotify-tools`.

## Next steps

- New to magus? Start with [Targets](../../concepts/targets.md) and [Spells](../../concepts/spells.md).
- Prefer a native binary? See the [install guides](../download.md#install).
