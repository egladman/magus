---
title: Install on Linux
description: Download, verify, and install the magus binary on Linux (amd64 or arm64) and put it on your PATH - no root, no sudo.
tags: [download, install, linux, path]
---

# Install on Linux

magus ships as a single self-contained binary. Download it with `curl`, extract it into a `PATH` directory you own - no root, no `sudo` - then [verify it](../download.md#verify-a-release) before first run.

## Quick install

```sh
VERSION=v0.1.0
ARCH=amd64            # or arm64 on ARM
curl -fLO "https://github.com/egladman/magus/releases/download/${VERSION}/magus_${VERSION}_linux_${ARCH}.tar.gz"
mkdir -p ~/.local/bin
tar -xzf "magus_${VERSION}_linux_${ARCH}.tar.gz"
mv magus ~/.local/bin/
magus version
```

`${VERSION}` above is the current release. Every build - both `amd64`/`arm64` and the statically linked `-static` variants for musl or minimal images - is listed at [/public/release/](../../public/release/).

## Verify the download

Fetch the manifest and its signature next to the tarball:

```sh
curl -fLO "https://github.com/egladman/magus/releases/download/${VERSION}/SHA256SUMS"
curl -fLO "https://github.com/egladman/magus/releases/download/${VERSION}/SHA256SUMS.sig"
```

Then verify the Ed25519 signature *first*, and only then the checksum - checking a hash against an unverified manifest proves nothing. The exact commands are in [Verify a release](../download.md#verify-a-release).

## Put it on your PATH

If `magus version` prints `command not found`, the install directory is not on your `PATH`. Add it once, in your shell rc:

```sh
# bash or zsh: append to ~/.bashrc or ~/.zshrc
export PATH="$HOME/.local/bin:$PATH"
```

Open a new shell afterward, then re-run `magus version`.

## Next steps

- [Verify the release](../download.md#verify-a-release) before first run.
- Set up [shell completion](../download.md#shell-completion) and the [`mgs` shorthand](../download.md#mgs-shorthand).
- Keep it current with [`magus self update`](../download.md#update).
