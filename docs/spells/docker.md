---
title: docker spell
description: "Docker spell: image build, build-check, buildx, and hadolint Dockerfile linting."
tags: [docker, spell, container, image, hadolint, tools]
---

# docker

The `docker` spell forks the `docker` CLI (and `hadolint`) to build images and lint Dockerfiles. `docker-build-check` runs the builder's `--check` preflight without producing an image.

**Runtime name:** `docker` (source `spells/docker/`)

**Version probe:** `docker --version`

## Passing arguments to ops

Every op is invoked as `docker["<op>"](opts?)`, where the optional options map accepts these keys - all optional, each appended to or shaping the forked command:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `docker["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L108) |
| `cwd` | `str` | Working directory the command runs in. Defaults to the project directory. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L105) |
| `env` | `{str: str}` | Environment variables set for the process, on top of the inherited environment. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L112) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L120) |

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## docker-build

**Command:** `docker build`

## docker-build-check

**Command:** `docker build --check`

## docker-buildx

**Command:** `docker buildx build`

## hadolint

**Command:** `hadolint Dockerfile`

