---
title: Setup
page_type: overview
description: Install magus from a signed release, verify it, set up your shell, and keep it current with magus self update.
tags: [install, download, release, self-update, ed25519, verify, signing]
---

# Setup

magus ships as a single self-contained binary. No runtime, no package manager required.

## Install

```sh
curl --proto '=https' --tlsv1.2 -sSf https://eli.gladman.cc/magus/install -o install.sh
less install.sh
sh install.sh
```

Read the script before you run it. It downloads the current release, checks the signature, and installs the binary, the man pages, and the [`mgs` shorthand](setup/shell-setup.md#mgs-shorthand) under `~/.local`. `--dry-run` prints the whole plan without writing anything.

In a hurry, and willing to give a network response your shell? `curl ... | sh` works too:

```sh
curl --proto '=https' --tlsv1.2 -sSf https://eli.gladman.cc/magus/install | sh
```

### Other ways

| Route                                              | When                                                       |
| -------------------------------------------------- | ---------------------------------------------------------- |
| [Linux](setup/linux.md)                     | manual install, amd64 or arm64                        |
| [macOS](setup/macos.md)                     | manual install, Apple Silicon or Intel                |
| [Windows](setup/windows.md)                 | manual install, amd64 or arm64                        |
| [Container image](setup/container-image.md) | run from an OCI image, or extract the binary from one |
| [mise](setup/mise.md)                       | you already manage tool versions with mise            |
| [Build from source](setup/build-from-source.md) | you want a local build, or a `noselfupdate` build |

## Platform support

Every platform below gets a signed release archive built by the same pipeline, and every
published archive is the STATIC build - it links nothing, so it runs wherever its platform
runs. A dynamically linked build is supported but not published; see the per-platform
guides. They do not all get the same amount of testing, and it is more useful to say so
than to imply otherwise.

CI runs the full test suite on **linux/amd64 only**. Every other platform is built
by the release pipeline but not tested by it.

| Platform | Testing |
| --- | --- |
| linux/amd64 | Full test suite on every CI run. The only continuously tested platform. |
| darwin/arm64 | Not covered by CI, but it is the primary development platform, so the suite runs against it constantly by hand. |
| linux/arm64 | Not covered by CI. Built natively by the release pipeline; the release binary and the test suites have been executed on real arm64 hardware. |
| darwin/amd64 | Not covered by CI. Built natively by the release pipeline on an Intel runner, so it compiles and links, but **never executed**. |
| windows/amd64 | Not covered by CI. Built natively by the release pipeline, so it compiles and links, but **never executed**. |
| windows/arm64 | Not covered by CI. Cross-compiled, static only, **never executed** - the newest and least proven target. |

Two consequences worth knowing before you pick a build:

- On **both Windows targets** nothing has run the binary end to end. windows/amd64
  has shipped for several releases and so has field use behind it; windows/arm64 is
  new and has none. If something behaves oddly there, that is worth reporting
  rather than working around.
- On **all Windows builds**, the Buzz JIT is newly enabled (it used to be disabled
  on Windows entirely) and its machine-code path has not executed on any Windows
  machine here. If a magusfile produces a result that looks wrong on Windows, set
  `BUZZ_JIT=0` and re-run: if the answer changes, that is a JIT bug and a very
  valuable report. See the [gopherbuzz JIT
  notes](https://github.com/egladman/magus/blob/main/libs/gopherbuzz/README.md#which-platforms-this-has-actually-run-on)
  for the full matrix.

## Next steps

- **[Verify the release](setup/verify.md)** before first run. Every build ships an Ed25519-signed `SHA256SUMS`; on a first install, verify it by hand rather than with the binary you just downloaded.
- **[Set up your shell](setup/shell-setup.md)** for tab-completion (bash, zsh, fish, PowerShell) and the `mgs` shorthand.
- **[Uninstall](setup/uninstall.md)** lists every path to delete: the binary, the man pages, the XDG state and config directories, and the workspace cache.

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
