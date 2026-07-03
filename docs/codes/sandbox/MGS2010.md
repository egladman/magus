---
title: "MGS2010: sandbox policy mismatch"
description: Fires when a long-running daemon is asked to serve a workspace whose sandbox policy was not part of the union it applied at startup, either undeclared or a differing fingerprint.
tags: [MGS2010, sandbox, daemon, policy, workspace, landlock, fingerprint]
---

# MGS2010: sandbox policy mismatch

A long-running daemon was asked to serve a workspace whose sandbox policy is
not the one it already committed to. Either the workspace was never declared,
or its policy differs from the union the daemon applied at startup.

```text
[MGS2010] workspace not declared
  see: .../MGS2010.md: workspace "/home/user/other" is not in this daemon's declared list; add it to daemon.workspaces (magus.yaml) or MAGUS_DAEMON_WORKSPACES and restart the daemon
```

```text
[MGS2010] sandbox policy mismatch
  see: .../MGS2010.md: sandbox policy for workspace "/home/user/app" differs from the policy already applied to this daemon process (fingerprint a1b2c3d4e5f60718 vs 0f1e2d3c4b5a6978); restart the daemon to pick up new sandbox configuration
```

## Why

The kernel landlock restriction is process-global and irreversible: once a
process calls `landlock_restrict_self`, that confinement holds for its whole
lifetime and cannot be relaxed. A daemon that serves many workspaces therefore
computes the set-union of every declared workspace's sandbox policy at startup
and applies landlock exactly once.

That makes the applied policy fixed for the life of the process. Two things
can then conflict with it:

- **Undeclared workspace** (raised in the workspace registry). In declared
  mode the daemon only serves workspaces listed in `daemon.workspaces` or
  `MAGUS_DAEMON_WORKSPACES`. A request for any other root is rejected, because
  its paths were never folded into the union and landlock would deny them.

- **Fingerprint mismatch** (raised when applying the per-workspace policy).
  The workspace was declared, but its sandbox config now resolves to a
  different fingerprint than the union that is already applied. The kernel and
  binding layers would disagree, so magus fails closed rather than run under a
  policy that does not match the kernel restriction.

## Resolution

1. **Undeclared workspace:** add the root to the daemon's declared list and
   restart the daemon so the new policy is folded into the union:

   ```yaml
   daemon:
     workspaces:
       - /home/user/app
       - /home/user/other
   ```

   Or via the environment:

   ```sh
   MAGUS_DAEMON_WORKSPACES=/home/user/app:/home/user/other magus daemon
   ```

2. **Fingerprint mismatch:** you changed the sandbox configuration
   (`sandbox.allow`, `sandbox.env`, etc.) after the daemon started. Restart
   the daemon so it rebuilds and re-applies the union with the new policy. A
   running daemon cannot pick up sandbox changes in place, by design.
