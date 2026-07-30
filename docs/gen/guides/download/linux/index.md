---
title: Install on Linux
description: Download, verify, and install the magus binary on Linux (amd64 or arm64) and put it on your PATH - no root, no sudo.
tags: [download, install, linux, path]
---

# Install on Linux

magus ships as a single self-contained binary. Download it with `curl`, extract it into a `PATH` directory you own - no root, no `sudo` - then [verify it](verify.md) before first run.

## Quick install

```sh
VERSION=v0.3.0
ARCH=amd64            # or arm64 on ARM
curl -fLO "https://github.com/egladman/magus/releases/download/${VERSION}/magus_${VERSION}_linux_${ARCH}-static.tar.gz"
mkdir -p ~/.local/bin
tar -xzf "magus_${VERSION}_linux_${ARCH}-static.tar.gz"
mv magus ~/.local/bin/
magus version
```

`${VERSION}` above is the current release. This is the static build, the default for
the installer. Dynamic builds are also attached to each [GitHub release](https://github.com/egladman/magus/releases)
when you need one.

## Verify the download

Fetch the manifest and its signature next to the tarball:

```sh
curl -fLO "https://github.com/egladman/magus/releases/download/${VERSION}/SHA256SUMS"
curl -fLO "https://github.com/egladman/magus/releases/download/${VERSION}/SHA256SUMS.sig"
```

Then verify the Ed25519 signature *first*, and only then the checksum - checking a hash against an unverified manifest proves nothing. The exact commands are in [Verify a release](verify.md).

## Put it on your PATH

If `magus version` prints `command not found`, the install directory is not on your `PATH`. Add it once, in your shell rc:

```sh
# bash or zsh: append to ~/.bashrc or ~/.zshrc
export PATH="$HOME/.local/bin:$PATH"
```

Open a new shell afterward, then re-run `magus version`.

## Next steps

- [Verify the release](verify.md) before first run.
- Set up [shell completion](shell-setup.md#shell-completion), and add the [`mgs` shorthand](shell-setup.md#mgs-shorthand) with `magus self install-shorthand`.
- Keep it current with [`magus self update`](../download.md#update).
