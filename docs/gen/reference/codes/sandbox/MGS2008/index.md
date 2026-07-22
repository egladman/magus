---
title: "MGS2008: daemon socket withheld"
description: Fires at debug level when magus withholds the daemon socket vars from an op subprocess (independent of sandbox.enabled), or re-injects them into a recursive magus invocation.
tags: [MGS2008, sandbox, daemon, socket, env, recursive, security]
---

# MGS2008: daemon socket withheld from subprocesses

The daemon socket vars (`MAGUS_DAEMON_SOCKET`, `MAGUS_DAEMON_ADDRESS`) are
magus's own pool pointers. magus withholds them from every op subprocess and
re-hands them only to a nested `magus`. This code is logged, at debug level
only, on both halves of that contract: when a var is withheld from a child,
and when it is re-injected for a legitimate recursive `magus` invocation.

```text
[MGS2008] withheld magus daemon pointer(s) from op subprocess (done regardless of sandbox.enabled) (see https://github.com/egladman/magus/blob/main/docs/codes/sandbox/MGS2008.md)
  vars=[MAGUS_DAEMON_SOCKET]
[MGS2008] daemon socket injected into recursive magus invocation
  var=MAGUS_DAEMON_SOCKET
```

## Why

The daemon socket is unauthenticated: anything that can reach it can drive
the daemon. If those vars were inherited by every child, a compromised
third-party spell could connect to the daemon and issue commands. So magus
strips them from every op subprocess, alongside the other secret-bearing vars
(see `MGS2003.md`).

This withholding is **independent of `sandbox.enabled`**. With the sandbox on,
the vars are simply absent from the environment allowlist. But the sandbox is
off by default, and "off" rebuilds no environment - the child would otherwise
inherit the whole parent environment - so magus carries the withholding in an
explicit code path that runs either way. That is why turning the sandbox off
does not hand these pointers to spells, and why they can go missing from a
subprocess even in a workspace with no sandbox configured at all.

A nested `magus` call is the one exception. It is a recursive invocation of the
same trusted binary, and it genuinely needs the daemon to coordinate. For that
case magus re-injects `MAGUS_DAEMON_SOCKET` and `MAGUS_DAEMON_ADDRESS` as
explicit env overrides on the child, and records it with this code.

The log fires at debug (`-v`), not info, because a run spawns many ops (and a
fan-out many recursive invocations) and it would otherwise be pure noise at
default verbosity. It is an internal correctness note, not an error and not
user-actionable.

## Resolution

Nothing to do. This is the sandbox working as intended: the socket stays
hidden from spell subprocesses, and is handed only to nested magus processes
that need it.

If a subprocess of yours genuinely needs to reach the daemon and cannot see the
socket, that is this rule at work - a plain program is not a nested `magus` and
is not trusted with the unauthenticated socket. Reach for the pointer through a
recursive `magus` call instead. The withheld line explains a missing var; the
`already running under a parent magus` error, which now names the inherited
`MAGUS_DAEMON_SOCKET`, explains the opposite case where a program mistook the
inherited socket for adoption.

You will see these lines only when running at `-v` or higher. If you do not
want the noise, run at default verbosity.
