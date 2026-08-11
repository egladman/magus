---
title: os module
aliases: [modules/os]
description: "The machine and this process: platform triple, CPU count, hostname, the running magus binary, and the two members that shadow Buzz's own (exit, sleep)."
tags: [os, module, stdlib, magusfile]
---

# os

The machine and this process: platform triple, CPU count, hostname, the running magus binary, and the two members that shadow Buzz's own (exit, sleep). Running OTHER processes lives in the proc module.

> **Naming convention:** import the module under its bare name (`import "os"`), reach members with a backslash, and call methods in `camelCase`: `os\someMethod`.

## Methods

### withEnv

Add env vars to subprocesses os.exec/os.exec_sh start inside callback. Never touches the process's own environment - a lookup like os.env inside callback does not see them.

**Signature:** `os\withEnv(env, callback)` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L420)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `env` | `map[string]string` |  | |
| `callback` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L17) |  | |

### platform

Return the Docker/OCI platform triple: (os, arch, variant).

**Signature:** `os\platform() → string, string, string` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L239)

**Returns:** string, string, string

### exit

Abort the current run with the given exit code - typically after logging an error. Does NOT call os.Exit (that would kill a shared daemon); it raises, ending the target, and the code becomes magus's process exit status.

**Signature:** `os\exit(code)` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L231)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `code` | `int` |  | |

### sleep

Pause for the given number of milliseconds (fractional allowed), matching Buzz's os.sleep. Cancellable: if the run is interrupted it returns early with the cancellation error rather than blocking.

**Signature:** `os\sleep(ms)` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L207)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `ms` | `float64` |  | |

### numCpu

Return the number of logical CPUs available, for sizing a command's own internal parallelism (see os.with_slots).

**Signature:** `os\numCpu() → int` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L143)

**Returns:** int

### hostname

Return the host machine's name.

**Signature:** `os\hostname() → string` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L148)

**Returns:** string

### executable

Return the absolute path of the running magus binary. Pair it with fs.stat inside a long-lived watch loop to detect that the binary was rebuilt or upgraded underneath the process, which means any output it goes on to generate would be stale.

**Signature:** `os\executable() → string` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L160)

**Returns:** string

### retry

Call fn up to max times, retrying on error with exponential backoff; returns fn's value on success. opts: {backoff_ms:float (default 500), max_backoff_ms:float (default 30000)}.

**Signature:** `os\retry(max, fn, [opts]) → any` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L436)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `max` | `int` |  | |
| `fn` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L17) |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** any

