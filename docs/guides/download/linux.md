---
title: Install on Linux
description: Download, verify, and install the magus binary on Linux (amd64, arm64, or 32-bit ARM) and put it on your PATH - no root, no sudo.
tags: [download, install, linux, path]
---

# Install on Linux

magus ships as a single self-contained binary. Download it with `curl`, extract it into a `PATH` directory you own - no root, no `sudo` - then [verify it](verify.md) before first run.

## Quick install

```sh
VERSION=__MAGUS_VERSION__
ARCH=amd64            # or arm64, armv7, armv6 - see below
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
| `armv7` | 32-bit ARM, VFPv3: Raspberry Pi 2 and up on a 32-bit OS, most modern boards. |
| `armv6` | Older 32-bit ARM, VFPv1: Raspberry Pi 1 and Pi Zero / Zero W.        |

`uname -m` reports the kernel's architecture: `x86_64` is `amd64`, `aarch64` is `arm64`,
`armv7l` is `armv7`, and `armv6l` is `armv6`. On ARM the kernel alone does not settle it -
a 64-bit Pi kernel often runs a 32-bit userland, and there `uname -m` says `aarch64` while
the machine needs a 32-bit archive. Decide in two steps:

1. **Userland width** - `dpkg --print-architecture` (or `getconf LONG_BIT`): `arm64` (64)
   means take `arm64` and stop; `armhf` (32) means a 32-bit archive, next step.
2. **Which 32-bit build** - by hardware: Raspberry Pi 1, Zero, and Zero W are ARMv6, so
   take `armv6`; every later Pi and most other boards take `armv7`. Do not read the ARM
   level out of `armhf` itself: on Debian proper `armhf` implies ARMv7, but Raspberry Pi
   OS uses the same name for its ARMv6 baseline.

Take `armv7` over `armv6` whenever the hardware supports it: it targets VFPv3 and the
wider ARMv7 instruction set, so it is faster. The `armv6` build is still hardware-float
(VFPv1); what it gives up is the newer instructions, not the FPU. Both 32-bit builds run
the Buzz interpreter rather than its JIT, which is 64-bit only.

`${VERSION}` above is the current release. The unsuffixed archive is the static build
and the installer default; it links nothing, so it runs on musl and glibc alike. The
`-cgo` archive attached to each [GitHub release](https://github.com/egladman/magus/releases)
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
