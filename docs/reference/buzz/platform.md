---
title: platform module
generated_from: reference/buzz/
aliases: [modules/platform]
description: Normalize OS/architecture identifiers across naming conventions (aarch64<->arm64, Darwin<->darwin).
tags: [platform, module, stdlib, magusfile]
---

# platform

Normalize OS/architecture identifiers across naming conventions (aarch64<->arm64, Darwin<->darwin).

> **Naming convention:** import the module under its bare name (`import "platform"`), reach members with a backslash, and call methods in `camelCase`: `platform\someMethod`.

## Methods

### arch

Normalize an architecture identifier (x86_64, aarch64, armv7l, ...) to canonical Go GOARCH (amd64, arm64, arm). With style, render that result in a convention (go|uname); raises on an unknown style. Returns "" when the identifier is unrecognized.

**Signature:** `platform\arch(name, [style]) -> string` - [source](https://github.com/egladman/magus/blob/main/std/platform.go#L229)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |
| `style` | `string` | yes | |

**Returns:** string

### os

Normalize an OS identifier (Darwin, macOS, win, ...) to canonical Go GOOS (darwin, windows). With style, render that result in a convention (go|uname); raises on an unknown style. Returns "" when the identifier is unrecognized.

**Signature:** `platform\os(name, [style]) -> string` - [source](https://github.com/egladman/magus/blob/main/std/platform.go#L239)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |
| `style` | `string` | yes | |

**Returns:** string

### memoryBytes

How much memory this process may commit, in BYTES, or 0 when it cannot be determined (any host other than Linux or macOS). Narrowed by a container's memory ceiling where there is one, the way cpus() honors a CPU quota. Note magus.project targets take memory_mb in MEGABYTES. Size work that scales on memory rather than cores with this: `go test` defaults its package parallelism to the CPU count, which is the wrong axis under -race, where each test binary carries the race detector's shadow memory. Branch on 0 rather than treating it as "no memory".

**Signature:** `platform\memoryBytes() -> int` - [source](https://github.com/egladman/magus/blob/main/std/platform.go#L148)

**Returns:** int

### cpus

How many CPUs this process may use (Go's GOMAXPROCS, which honors a container quota where the OS-visible core count does not). Pair with memory_bytes() when sizing parallel work: the smaller of the two limits is the one that matters.

**Signature:** `platform\cpus() -> int` - [source](https://github.com/egladman/magus/blob/main/std/platform.go#L160)

**Returns:** int

