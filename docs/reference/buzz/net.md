---
title: net module
generated_from: reference/buzz/
aliases: [modules/net]
description: "TCP readiness and port allocation: wait for a service to accept connections, and find a free port."
tags: [net, module, stdlib, magusfile]
---

# net

TCP readiness and port allocation: wait for a service to accept connections, and find a free port.

> **Naming convention:** import the module under its bare name (`import "net"`), reach members with a backslash, and call methods in `camelCase`: `net\someMethod`.

## Methods

### waitForPort

Block until a TCP connection to host:port succeeds, then return true; return false when the timeout elapses first. Use it after starting a dev server or a container instead of sleeping a guessed number of seconds. timeout_ms defaults to 30000. Returns FALSE rather than raising on timeout, so a caller can fall back or report its own message; the run's cancellation is honored, so Ctrl-C does not wait out the timeout.

**Signature:** `net\waitForPort(host, port, [timeout_ms]) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/net.go#L124)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `host` | `string` |  | |
| `port` | `int` |  | |
| `timeout_ms` | `int` | yes | |

**Returns:** bool

### isPortOpen

Report whether a TCP connection to host:port succeeds right now, with no waiting. The single-shot form of wait_for_port - for deciding whether a service is ALREADY running before starting another one.

**Signature:** `net\isPortOpen(host, port) -> bool` - [source](https://github.com/egladman/magus/blob/main/std/net.go#L98)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `host` | `string` |  | |
| `port` | `int` |  | |

**Returns:** bool

### freePort

Ask the operating system for an unused TCP port and return it. Bind it promptly: the port is released before this returns, so between the call and your server's own bind another process could take it. That race is unavoidable for any "find a free port" answer and is why this is for choosing a dev-server port, not for anything that must not collide.

**Signature:** `net\freePort() -> int` - [source](https://github.com/egladman/magus/blob/main/std/net.go#L163)

**Returns:** int

