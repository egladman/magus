---
title: Install on Windows
description: Download, verify, and install the magus binary on Windows (amd64) with PowerShell and put it on your PATH.
tags: [download, install, windows, powershell, path]
---

# Install on Windows

magus ships as a single self-contained binary. Download it with `curl.exe`, extract it into a `PATH` directory you own, then [verify it](../download.md#verify-a-release) before first run. Run these in PowerShell:

## Quick install

```powershell
$VERSION = "__MAGUS_VERSION__"
curl.exe -fLO "https://github.com/egladman/magus/releases/download/$VERSION/magus_${VERSION}_windows_amd64.tar.gz"
mkdir -Force $Env:USERPROFILE\bin | Out-Null
tar -xzf "magus_${VERSION}_windows_amd64.tar.gz"
Move-Item -Force magus.exe $Env:USERPROFILE\bin\magus.exe
magus version
```

Both `curl.exe` and `tar` ship with Windows 10 (1803+) and Windows 11, so no extra tooling is needed. `$VERSION` above is the current release; [/public/release/](../../public/release/) lists every build.

## Verify the download

Fetch the manifest and its signature next to the tarball:

```powershell
curl.exe -fLO "https://github.com/egladman/magus/releases/download/$VERSION/SHA256SUMS"
curl.exe -fLO "https://github.com/egladman/magus/releases/download/$VERSION/SHA256SUMS.sig"
```

Then verify the Ed25519 signature *first*, and only then the checksum - checking a hash against an unverified manifest proves nothing. The exact commands are in [Verify a release](../download.md#verify-a-release).

## Put it on your PATH

If `magus version` is not found, add the install directory to your user `PATH` (persists across sessions):

```powershell
[Environment]::SetEnvironmentVariable("Path", "$Env:USERPROFILE\bin;$Env:Path", "User")
```

Open a new PowerShell window afterward, then re-run `magus version`.

## Next steps

- [Verify the release](../download.md#verify-a-release) before first run.
- Set up [shell completion](../download.md#shell-completion) (PowerShell is supported).
- Keep it current with [`magus self update`](../download.md#update).
