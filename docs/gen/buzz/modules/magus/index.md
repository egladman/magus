---
title: magus module
aliases: [modules/magus]
description: Magus core primitives.
tags: [magus, module, stdlib, magusfile]
---

# magus

Magus core primitives.

> **Naming convention:** import the module under its bare name (`import "magus"`) and call methods in `camelCase` (`magus.someMethod`).

## Methods

### cmd

Escape hatch: run `magus <args>` for any subcommand, in the target's project directory. Prefer the dedicated methods (run, describe, insight, doctor) when one exists — magus.cmd warns when args name a subcommand that has one. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.quiet captures the output without echoing it to the console.

**Signature:** `magus.cmd(args, [opts]) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L138)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### run

Run `magus run <args>` recursively in the target's project directory and capture its output. Child invocations share the parent's concurrency budget over the local socket. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.quiet captures the output without echoing it to the console.

**Signature:** `magus.run(args, [opts]) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L155)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### describe

Run `magus describe <args>` in the target's project directory and capture its output. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.quiet captures the output without echoing it to the console. Unlike a raw binary call, the working directory is always the contextual project dir, so a nested project describes itself, not the root workspace.

**Signature:** `magus.describe(args, [opts]) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L160)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### insight

Run `magus insight <args>` in the target's project directory and capture its output. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.quiet captures the output without echoing it to the console.

**Signature:** `magus.insight(args, [opts]) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L165)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### doctor

Run `magus doctor <args>` in the target's project directory and capture its output. Returns {stdout, stderr, code, ok}; raises on non-zero exit (catch for non-fatal use). opts.root sets the global --root workspace; opts.quiet captures the output without echoing it to the console.

**Signature:** `magus.doctor(args, [opts]) → map[string]any` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L170)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `args` | `[]string` |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### bust_cache

Invalidate the build cache. Escape hatch — prefer modeling missing inputs as Sources. No arg clears all; a project path clears one project.

**Signature:** `magus.bustCache([project_path])` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L114)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `project_path` | `string` | yes | |

### has_charm

True when execution charm `name` is active, letting a target body branch on a charm carried in context (e.g. has_charm("rw")).

**Signature:** `magus.has_charm(name) → bool` · [source](https://github.com/egladman/magus/blob/main/std/magus.go#L107)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |

**Returns:** bool

