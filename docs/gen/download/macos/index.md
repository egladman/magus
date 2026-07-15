---
title: Install on macOS
description: Download, verify, and install the magus binary on macOS (Apple Silicon or Intel), clear the quarantine flag, and put it on your PATH.
tags: [download, install, macos, apple silicon, quarantine, path]
---

# Install on macOS

magus ships as a single self-contained binary. Grab your architecture's tarball from [/public/release/](../../public/release/), [verify it](../download.md#verify-a-release), and extract it into a `PATH` directory you own - no root, no `sudo`.

```sh
mkdir -p ~/.local/bin
tar -xzf magus_*_darwin_arm64.tar.gz   # or darwin_amd64 on Intel
mv magus ~/.local/bin/
magus version
```

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
