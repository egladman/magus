---
title: platform module
description: Normalize OS and architecture identifiers across naming conventions (aarch64 to arm64, Darwin to darwin) and render them in go or uname style.
tags: [platform, os, arch, goarch, goos, uname, normalize, magus stdlib]
---

# platform

Normalize OS/architecture identifiers across naming conventions (aarch64↔arm64, Darwin↔darwin).

> **Naming convention:** import the module under its bare name (`import "platform"`) and call methods in `camelCase` (`platform.someMethod`).

## Methods

### arch

Normalize an architecture identifier (x86_64, aarch64, armv7l, …) to canonical Go GOARCH (amd64, arm64, arm). With style, render that result in a convention (go|uname); raises on an unknown style. Returns "" when the identifier is unrecognized.

**Signature:** `platform.arch(name, [style]) → string` · [source](https://github.com/egladman/magus/blob/main/std/platform.go#L180)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |
| `style` | `string` | yes | |

**Returns:** string

### os

Normalize an OS identifier (Darwin, macOS, win, …) to canonical Go GOOS (darwin, windows). With style, render that result in a convention (go|uname); raises on an unknown style. Returns "" when the identifier is unrecognized.

**Signature:** `platform.os(name, [style]) → string` · [source](https://github.com/egladman/magus/blob/main/std/platform.go#L190)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `name` | `string` |  | |
| `style` | `string` | yes | |

**Returns:** string

