---
title: Install on Windows
description: Download, verify, and install the magus binary on Windows (amd64) with PowerShell and put it on your PATH.
tags: [download, install, windows, powershell, path]
---

# Install on Windows

magus ships as a single self-contained binary. Grab the tarball from [/public/release/](../../public/release/), [verify it](../download.md#verify-a-release), and extract it into a `PATH` directory you own. Run these in PowerShell:

```powershell
mkdir -Force $Env:USERPROFILE\bin | Out-Null
tar -xzf magus_*_windows_amd64.tar.gz
Move-Item magus.exe $Env:USERPROFILE\bin\magus.exe
magus version
```

`tar` ships with Windows 10 (1803+) and Windows 11, so no extra tooling is needed.

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
