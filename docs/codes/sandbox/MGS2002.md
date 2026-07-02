---
title: "MGS2002: path write denied"
description: Fires when a spell tries to write to a file outside the workspace and outside the sandbox allowlist, blocking persistence and tampering attacks.
tags: [MGS2002, sandbox, security, permissions, write, allowlist, persistence]
---

# MGS2002: path write denied by sandbox

A spell tried to write to a file outside the workspace and outside the
configured allowlist.

```text
[MGS2002] fs write denied: /home/user/.bashrc
```

## Why

The sandbox denies writes outside `<workspace>` and `/tmp` by default.
This blocks the supply-chain attack pattern of modifying shell startup
files, sudoers, or cron tables for persistence.

## Resolution

1. **If the write target is a legitimate cache** (e.g. a Rust spell
   writing to `~/.cargo/registry/cache`): add it to `magus.yaml`:

   ```yaml
   sandbox:
     allow:
       - path: ~/.cargo
         mode: rw
   ```

2. **If the spell should be writing inside the workspace** but is using
   an absolute path: the spell has a bug. File an issue with the spell
   author; in the meantime grant the path explicitly or disable sandbox
   mode.

3. **If this is a malicious spell**: do not grant the path. Uninstall.
