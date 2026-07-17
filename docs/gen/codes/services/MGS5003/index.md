---
title: "MGS5003: command op never exits"
description: Fires when a command op runs a watcher (tsc --watch, vitest --watch), which never terminates, so a run-to-completion op would hang the run. The mirror image of MGS5002.
tags: [MGS5003, services, command op, watch, ward]
---

# MGS5003: command op never exits

A command [op](../../operations.md) runs a known watch tool with `--watch` (for
example `tsc --watch`, `vitest --watch`, or `npx tsc --watch`). A command op is
meant to run to completion, but a watcher never terminates, so the run would hang
waiting for it. magus rejects it at resolution.

```text
[MGS5003] command op "typecheck" runs a watcher with "--watch": a command op runs to completion, so a never-exiting watch process hangs the run. Make this a service op instead.
  see: .../MGS5003.md
```

## Why

This is the mirror image of [MGS5002](MGS5002.md): the argv contradicts the op's
declared kind. There, a service (long-running) op detaches; here, a command (run to
completion) op never exits. Both are the same bug from opposite ends - the argv
lies about the kind - and both are errors with no flag-level suppression, because
the fix is to change the kind, not silence the check.

The check is scoped to known watch tools (`tsc`, `vitest`, `jest`, `vite`,
`webpack`, `rollup`, `esbuild`, `cargo-watch`), looking through common runners
(`npx`, `pnpm`, `yarn`, `bunx`) to the tool they invoke, so a `--watch` that means
something else on an unrelated tool is not misread.

## Fix

Make it a service op. A watcher is a long-running process, which is exactly what a
service op models: magus forks it in the foreground, blocks on it, and can attach a
readiness probe and a stop command. Return a `Service{command = ...}` instead of a
`Command`.
