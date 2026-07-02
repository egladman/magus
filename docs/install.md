---
title: Install magus
description: Install magus from a prebuilt release or build it from source with Go, then keep it current via magus self update or your package manager.
tags: [install, installation, binary, release, self-update, setup, build-from-source]
---

# Install

magus is a single self-contained binary. Install a prebuilt release, or build it from source.

## From a release binary

Download the latest release for your platform from the [releases page](https://github.com/egladman/magus/releases), extract the archive, and move the binary onto your `PATH`:

```sh
mv magus ~/.local/bin/magus
```

Make sure the destination directory is on your `PATH` (for example, add `export PATH="$HOME/.local/bin:$PATH"` to your shell profile). Enable tab completion with `magus completion <shell>`.

## From source

With a recent Go toolchain:

```sh
git clone https://github.com/egladman/magus
cd magus
go build -o magus ./cmd/magus
```

`magus self update` is compiled in by default. Build with `-tags noselfupdate`
to disable the self-update mechanism (e.g. for package-manager-owned binaries).

## Keeping up to date

Once installed, update in place with:

```sh
magus self update
```
