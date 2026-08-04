---
title: Download
page_type: overview
description: Install magus from a signed release, verify it, set up your shell, and keep it current with magus self update.
tags: [download, install, release, self-update, ed25519, verify, signing]
---

# Download

magus ships as a single self-contained binary. No runtime, no package manager required.

## Install

```sh
curl --proto '=https' --tlsv1.2 -sSf https://eli.gladman.cc/magus/install -o install.sh
less install.sh
sh install.sh
```

Read the script before you run it. It downloads the current release, checks the signature, and installs the binary, the man pages, and the [`mgs` shorthand](download/shell-setup.md#mgs-shorthand) under `~/.local`. `--dry-run` prints the whole plan without writing anything.

In a hurry, and willing to give a network response your shell? `curl ... | sh` works too:

```sh
curl --proto '=https' --tlsv1.2 -sSf https://eli.gladman.cc/magus/install | sh
```

### Other ways

| Route                                              | When                                                       |
| -------------------------------------------------- | ---------------------------------------------------------- |
| [Linux](download/linux.md)                          | manual install, amd64 or arm64                              |
| [macOS](download/macos.md)                          | manual install, Apple Silicon or Intel                      |
| [Windows](download/windows.md)                      | amd64                                                       |
| [Container image](download/container-image.md)      | run from an OCI image, or extract the binary from one       |
| [mise](download/package-managers.md)                | you already manage tool versions with mise                  |
| [Build from source](download/package-managers.md#build-from-source) | you want a local build, or a `noselfupdate` build |

## Next steps

- **[Verify the release](download/verify.md)** before first run. Every build ships an Ed25519-signed `SHA256SUMS`; on a first install, verify it by hand rather than with the binary you just downloaded.
- **[Set up your shell](download/shell-setup.md)** for tab-completion (bash, zsh, fish, PowerShell) and the `mgs` shorthand.
- **[Uninstall](download/uninstall.md)** lists every path to delete: the binary, the man pages, the XDG state and config directories, and the workspace cache.

## Update

`magus self update` fetches the latest release, verifies the signature against the key baked into your binary, and swaps in place. Full flag reference: [`magus self`](../reference/manpage/magus-self/).

| Flag               | Effect                                 |
| ------------------ | -------------------------------------- |
| `--check`          | Report availability without installing |
| `--dry-run`        | Fetch and verify but do not swap       |
| `--version v0.4.2` | Pin to a specific tag                  |
| `--force`          | Allow downgrade or reinstall           |
| `--bin-dir <path>` | Install elsewhere instead of in place  |
| `-y` / `--yes`     | Skip the confirmation prompt           |

Package-maintainer builds compiled with `-tags noselfupdate` disable this subcommand; fall back to a manual install.

Release notes are in the [CHANGELOG](https://github.com/egladman/magus/blob/main/CHANGELOG.md).
