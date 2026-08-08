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
go build -o magus ./cmd/magus
```

Add `-tags noselfupdate` to disable the self-update subcommand (for distro-packaged builds).
