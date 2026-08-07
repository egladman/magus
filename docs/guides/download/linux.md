---
title: Install on Linux
description: Download, verify, and install the magus binary on Linux (amd64 or arm64) and put it on your PATH - no root, no sudo.
tags: [download, install, linux, path]
---

# Install on Linux

magus ships as a single self-contained binary. Download it with `curl`, extract it into a `PATH` directory you own - no root, no `sudo` - then [verify it](verify.md) before first run.

## Quick install

```sh
VERSION=__MAGUS_VERSION__
ARCH=amd64            # or arm64 - see below
curl -fLO "https://github.com/egladman/magus/releases/download/${VERSION}/magus_${VERSION}_linux_${ARCH}.tar.gz"
mkdir -p ~/.local/bin
tar -xzf "magus_${VERSION}_linux_${ARCH}.tar.gz"
mv magus ~/.local/bin/
magus version
```

## Which `ARCH`

| `ARCH`  | Hardware                                                            |
| ------- | ------------------------------------------------------------------- |
| `amd64` | Any 64-bit x86 machine.                                              |
| `arm64` | 64-bit ARM: Raspberry Pi 3 and up on a 64-bit OS, Ampere, Graviton.  |

`uname -m` reports the kernel's architecture: `x86_64` is `amd64` and `aarch64` is
`arm64`. On ARM the kernel alone does not settle it - a 64-bit Pi kernel often runs a
32-bit userland, and there `uname -m` says `aarch64` while the userland is 32-bit.
Check with `dpkg --print-architecture` (or `getconf LONG_BIT`): `arm64` (64) means take
`arm64`; `armhf` (32) means there is no archive for this machine yet.

No 32-bit ARM archive is published. The build works, but nothing has run it end to end
on 32-bit hardware or under emulation, and an untested binary is worse than an absent
one. If you need one, build from source.

`${VERSION}` above is the current release. The unsuffixed archive is the static build
and the installer default; it links nothing, so it runs on musl and glibc alike. The
`_dynamic` archive attached to each [GitHub release](https://github.com/egladman/magus/releases)
is the glibc build, and it is the one to take if a magusfile calls Buzz FFI (`zdef()`),
which the static build compiles out.

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
