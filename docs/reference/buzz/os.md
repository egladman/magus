---
title: os module
aliases: [modules/os]
description: Process execution.
tags: [os, module, stdlib, magusfile]
---

# os

Process execution. os.exec runs a command directly (no shell); os.exec_sh runs a line through the shell. Both stream output live and return a result {stdout, stderr, code, ok}.

> **Naming convention:** import the module under its bare name (`import "os"`), reach members with a backslash, and call methods in `camelCase`: `os\someMethod`.

## Methods

### exec

Run cmd directly (no shell; args are never shell-interpolated). Output streams live and is captured. Returns {stdout, stderr, code, ok}; raises on non-zero exit unless opts.allow_failure is true. Optional dir runs cmd there (relative to the target's cwd). opts.stdin is fed to the process as standard input - pipe by passing a prior call's stdout.

**Signature:** `os\exec(cmd, [args], [dir], [opts]) → ExecResult` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L379)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `cmd` | `string` |  | |
| `args` | `[]string` | yes | |
| `dir` | `string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### execSh

Run line through a shell - for pipes, redirection, globs, and variable expansion. Default shell is /bin/sh (cmd on Windows); pass opts.shell (e.g. "bash") to override, resolved via PATH. A shell line is written in the platform shell's dialect, so sh and cmd lines are not portable across OSes - for cross-platform logic prefer os.exec plus the fs/os helpers. Same result and raise semantics as exec (opts.stdin and opts.allow_failure included); optional dir runs the shell there.

**Signature:** `os\execSh(line, [dir], [opts]) → ExecResult` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L393)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `line` | `string` |  | |
| `dir` | `string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### withEnv

Set env vars for the duration of callback; restore after.

**Signature:** `os\withEnv(env, callback)` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L439)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `env` | `map[string]string` |  | |
| `callback` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L17) |  | |

### withSlots

Reserve n slots from magus's concurrency budget for the duration of callback. Use when callback runs a command with its own internal parallelism (make -j, a test runner) that magus can't see, so the global budget is not oversubscribed.

**Signature:** `os\withSlots(n, callback)` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L519)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `n` | `int` |  | |
| `callback` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L17) |  | |

### platform

Return the Docker/OCI platform triple: (os, arch, variant).

**Signature:** `os\platform() → string, string, string` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L280)

**Returns:** string, string, string

### exit

Abort the current run with the given exit code - typically after logging an error. Does NOT call os.Exit (that would kill a shared daemon); it raises, ending the target, and the code becomes magus's process exit status.

**Signature:** `os\exit(code)` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L272)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `code` | `int` |  | |

### sleep

Pause for the given number of milliseconds (fractional allowed), matching Buzz's os.sleep. Cancellable: if the run is interrupted it returns early with the cancellation error rather than blocking.

**Signature:** `os\sleep(ms)` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L248)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `ms` | `float64` |  | |

### which

Resolve cmd against PATH and return its absolute path. RAISES when the command is not found - wrap it in try/catch to check a tool is installed and emit a clear hint instead of a cryptic exec failure.

**Signature:** `os\which(cmd) → string` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L235)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `cmd` | `string` |  | |

**Returns:** string

### stdinIsTerminal

Report whether standard input is a terminal (TTY) rather than a pipe, file, or /dev/null. Use it to fail fast with a clear message instead of blocking on a read of stdin that will never receive piped input.

**Signature:** `os\stdinIsTerminal() → bool` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L221)

**Returns:** bool

### numCpu

Return the number of logical CPUs available, for sizing a command's own internal parallelism (see os.with_slots).

**Signature:** `os\numCpu() → int` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L184)

**Returns:** int

### hostname

Return the host machine's name.

**Signature:** `os\hostname() → string` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L189)

**Returns:** string

### executable

Return the absolute path of the running magus binary. Pair it with fs.stat inside a long-lived watch loop to detect that the binary was rebuilt or upgraded underneath the process, which means any output it goes on to generate would be stale.

**Signature:** `os\executable() → string` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L201)

**Returns:** string

### retry

Call fn up to max times, retrying on error with exponential backoff; returns fn's value on success. opts: {backoff_ms:float (default 500), max_backoff_ms:float (default 30000)}.

**Signature:** `os\retry(max, fn, [opts]) → any` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L455)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `max` | `int` |  | |
| `fn` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L17) |  | |
| `opts` | `map[string]any` | yes | |

**Returns:** any

