---
title: Install on macOS
description: Download, verify, and install the magus binary on macOS (Apple Silicon or Intel), clear the quarantine flag, and put it on your PATH.
tags: [download, install, macos, apple silicon, quarantine, path]
---

# Install on macOS

magus ships as a single self-contained binary. Download it with `curl`, extract it into a `PATH` directory you own - no root, no `sudo` - then [verify it](../download.md#verify-a-release) before first run.

## Quick install

```sh
VERSION=v0.2.1
ASSET=magus_${VERSION}_darwin_arm64.tar.gz              # Apple Silicon
# Intel Macs use the statically linked build:
#   ASSET=magus_${VERSION}_darwin_amd64-static.tar.gz
curl -fLO "https://github.com/egladman/magus/releases/download/${VERSION}/${ASSET}"
mkdir -p ~/.local/bin
tar -xzf "${ASSET}"
mv magus ~/.local/bin/
magus version
```

`${VERSION}` above is the current release; [/public/release/](../../../public/release/) lists every build, including the statically linked `-static` variants.

## Verify the download

Fetch the manifest and its signature next to the tarball:

```sh
curl -fLO "https://github.com/egladman/magus/releases/download/${VERSION}/SHA256SUMS"
curl -fLO "https://github.com/egladman/magus/releases/download/${VERSION}/SHA256SUMS.sig"
```

Then verify the Ed25519 signature *first*, and only then the checksum - checking a hash against an unverified manifest proves nothing. The exact commands (macOS uses `shasum -a 256`) are in [Verify a release](../download.md#verify-a-release).

## Clear the quarantine flag

If macOS blocks the binary ("cannot be opened, unidentified developer"), strip the quarantine attribute Gatekeeper added on download:

```sh
xattr -d com.apple.quarantine ~/.local/bin/magus
```

## Put it on your PATH

If `magus version` prints `command not found`, the install directory is not on your `PATH`. Add it once, in your shell rc:

```sh
# zsh (default) or bash: append to ~/.zshrc or ~/.bashrc
export PATH="$HOME/.local/bin:$PATH"
```

Open a new shell afterward, then re-run `magus version`.

## Next steps

- [Verify the release](../download.md#verify-a-release) before first run.
- Set up [shell completion](../download.md#shell-completion) and the [`mgs` shorthand](../download.md#mgs-shorthand).
- Keep it current with [`magus self update`](../download.md#update).
