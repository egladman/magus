---
title: Build from source
description: Build magus locally with go build, including the noselfupdate, liblzma, and libzstd build tags.
tags: [build-from-source, go-build, noselfupdate, liblzma, libzstd, packaging]
---

# Build from source

Building locally trades away the signed-release guarantee of the
[install script](../setup.md#install): you get whatever your checkout and your
toolchain produce, verified by nothing.

```sh
git clone https://github.com/egladman/magus
cd magus
GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .
```

That one command compiles magus and has it run its own `go-build` target, which
regenerates the embedded spells and links a stamped `./magus`. `--no-cache`
skips the magus cache, whose key for that target cannot express the embedded
spell ordering; Go's build cache stays on, and `-trimpath` matches the target's
own build so packages compile once. `GOEXPERIMENT=jsonv2` is on the line because
magus refuses to compile without it. Use `./magus` from then on.

A distro package that links with `go build` adds `-tags noselfupdate` to disable the self-update subcommand.
