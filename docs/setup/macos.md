---
title: Install on macOS
description: Download, verify, and install the magus binary on macOS (Apple Silicon or Intel), clear the quarantine flag, and put it on your PATH.
tags: [download, install, macos, apple silicon, quarantine, path]
---

# Install on macOS

> [!NOTE]
> Neither macOS target is covered by CI. darwin/arm64 is the primary development platform, so
> the suite runs against it constantly by hand. darwin/amd64 is built natively on an Intel
> runner, so it compiles and links, but is **never executed**.
>
> See [platform support](../setup.md#platform-support) for the full matrix.

magus ships as a single self-contained binary. Download it with `curl`, extract it into a `PATH` directory you own - no root, no `sudo` - then [verify it](verify.md) before first run.

## Quick install

```sh
VERSION=__MAGUS_VERSION__
ASSET=magus_${VERSION}_darwin_arm64_static.tar.gz       # Apple Silicon
# Intel Macs:
#   ASSET=magus_${VERSION}_darwin_amd64_static.tar.gz
curl -fLO "https://github.com/egladman/magus/releases/download/${VERSION}/${ASSET}"
mkdir -p ~/.local/bin
tar -xzf "${ASSET}" magus
mv magus ~/.local/bin/
magus version
```

The archive also carries `LICENSE`, `THIRD-PARTY-NOTICES`, `README.md`, and a
`BUILDINFO` file naming the exact version, commit, platform, and variant. Naming
`magus` on the `tar` line above extracts just the binary; drop it to unpack all of
them. `BUILDINFO` is readable without running anything, which is the point if a
dynamically linked build will not start.

`${VERSION}` above is the current release. The `_static` archive is the installer
default and what `magus self update` fetches.

## Verify the download

Fetch the manifest and its signature next to the tarball:

```sh
curl -fLO "https://github.com/egladman/magus/releases/download/${VERSION}/SHA256SUMS"
curl -fLO "https://github.com/egladman/magus/releases/download/${VERSION}/SHA256SUMS.sig"
```

Then verify the Ed25519 signature _first_, and only then the checksum - checking a hash against an unverified manifest proves nothing. The exact commands (macOS uses `shasum -a 256`) are in [Verify a release](verify.md).

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

- [Verify the release](verify.md) before first run.
- Set up [shell completion](shell-setup.md#shell-completion), and add the [`mgs` shorthand](shell-setup.md#mgs-shorthand) with `magus self install-shorthand`.
- Keep it current with [`magus self update`](../setup.md#update).
