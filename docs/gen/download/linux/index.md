---
title: Install on Linux
description: Download, verify, and install the magus binary on Linux (amd64 or arm64) and put it on your PATH - no root, no sudo.
tags: [download, install, linux, path]
---

# Install on Linux

magus ships as a single self-contained binary. Grab your architecture's tarball from [/public/release/](../../public/release/), [verify it](../download.md#verify-a-release), and extract it into a `PATH` directory you own - no root, no `sudo`.

```sh
mkdir -p ~/.local/bin
tar -xzf magus_*_linux_amd64.tar.gz    # or linux_arm64 on ARM
mv magus ~/.local/bin/
magus version
```

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
