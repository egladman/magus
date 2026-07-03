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
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `docker["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L107) |
| `cwd` | `str` | Working directory the command runs in. Defaults to the project directory. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L104) |
| `env` | `{str: str}` | Environment variables set for the process, on top of the inherited environment. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L111) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L119) |

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## docker-build

**Command:** `docker build`

### Example

<!-- run-recorder -->
```buzz
// docker-build's base command is just `docker build`, so pass the image tag and
// build context: `magus run image` forks `docker build -t app:latest .`.
import "magus";
import "magus/spell/docker";

magus.project({ "spells": [docker] });

export fun image(args: [str]) > void {
    docker["docker-build"]({ "args": ["-t", "app:latest", "."] });
}
```

## docker-build-check

**Command:** `docker build --check`

### Example

<!-- run-recorder -->
```buzz
// docker-build-check runs the builder `--check` preflight over a context without
// producing an image; pass the build context (`.`).
import "magus";
import "magus/spell/docker";

magus.project({ "spells": [docker] });

export fun image_check(args: [str]) > void {
    docker["docker-build-check"]({ "args": ["."] });
}
```

## docker-buildx

**Command:** `docker buildx build`

### Example

<!-- run-recorder -->
```buzz
// docker-buildx builds with BuildKit; pass the tag and context. Add
// "--platform", "linux/amd64,linux/arm64" to the args for a multi-platform build.
import "magus";
import "magus/spell/docker";

magus.project({ "spells": [docker] });

export fun image_buildx(args: [str]) > void {
    docker["docker-buildx"]({ "args": ["-t", "app:latest", "."] });
}
```

## hadolint

**Command:** `hadolint Dockerfile`

### Example

<!-- run-recorder -->
```buzz
// hadolint lints the Dockerfile for common mistakes.
import "magus";
import "magus/spell/docker";

magus.project({ "spells": [docker] });

export fun lint(args: [str]) > void {
    docker["hadolint"]();
}
```

