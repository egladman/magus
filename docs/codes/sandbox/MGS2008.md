---
title: "MGS2008: daemon socket withheld"
description: Fires at debug level when magus re-injects the daemon socket vars into a recursive magus invocation, after withholding them from sandboxed spell subprocesses.
tags: [MGS2008, sandbox, daemon, socket, env, recursive, security]
---

# MGS2008: daemon socket withheld from sandboxed children

The daemon socket vars (`MAGUS_DAEMON_SOCKET`, `MAGUS_DAEMON_ADDRESS`) are
withheld from the environment of sandboxed spell subprocesses. This code is
logged, at debug level only, when magus re-injects them for a legitimate
recursive `magus` invocation.

```text
[MGS2008] daemon socket injected into recursive magus invocation (see https://github.com/egladman/magus/blob/main/docs/codes/sandbox/MGS2008.md)
  var=MAGUS_DAEMON_SOCKET
```

## Why

The daemon socket is unauthenticated: anything that can reach it can drive
the daemon. If those vars were inherited by every child, a compromised
third-party spell could connect to the daemon and issue commands. So the
sandbox child environment strips them by default, alongside the other
secret-bearing vars (see `MGS2003.md`).

A nested `magus` call is different. It is a recursive invocation of the same
trusted binary, and it genuinely needs the daemon to coordinate. For that one
case magus re-injects `MAGUS_DAEMON_SOCKET` and `MAGUS_DAEMON_ADDRESS` as
explicit env overrides on the child, and records it with this code.

The log fires at debug (`-v`), not info, because a fan-out can spawn many
recursive invocations and it would otherwise be pure noise at default
verbosity. It is an internal correctness note, not an error and not
user-actionable.

## Resolution

Nothing to do. This is the sandbox working as intended: the socket stays
hidden from spell subprocesses, and is handed only to nested magus processes
that need it.

You will see this line only when running at `-v` or higher. If you do not
want the noise, run at default verbosity.
