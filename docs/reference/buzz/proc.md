---
title: proc module
aliases: [modules/proc]
description: Run other processes.
tags: [proc, module, stdlib, magusfile]
---

# proc

Run other processes. proc.exec is the one verb that runs anything: it streams output live, captures it, honours the sandbox, and raises on failure instead of handing back a code to check. Needing a shell is not a second verb - proc.shell builds the {bin, args} to hand it, so which shell ran stays visible at the call site instead of hidden inside it. Distinct from Buzz's own os.execute, which returns an exit code and stays silent when you do not read it.

> **Naming convention:** import the module under its bare name (`import "proc"`), reach members with a backslash, and call methods in `camelCase`: `proc\someMethod`.

## Methods

### exec

Run cmd directly (no shell; args are never shell-interpolated). Output streams live and is captured. Returns {stdout, stderr, code, ok}; raises on non-zero exit unless opts.allow_failure is true. Optional dir runs cmd there (relative to the target's cwd). opts.stdin is fed to the process as standard input - pipe by passing a prior call's stdout. opts.quiet captures the output without echoing it to the console. opts.tty runs cmd on a pseudo-terminal so it behaves as it would for a person: tools that check isatty keep their colour and progress output instead of the plain form they emit to a pipe. A terminal is a single stream, so stderr arrives merged into stdout and the captured text carries ANSI escapes. Unix only.

**Signature:** `proc\exec(cmd, [args], [dir], [opts]) → ExecResult` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L352)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `cmd` | `string` |  | |
| `args` | `[]string` | yes | |
| `dir` | `string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### shell

Build the command line that runs `line` through the platform shell, WITHOUT running it: returns {bin, args} for proc.exec. Default shell is /bin/sh (cmd on Windows); pass shell (e.g. "bash") to override, resolved via PATH. This is a pure function, so the argv is inspectable before anything executes - print it, log it, or assert on it. A shell line is written in the platform shell's dialect, so sh and cmd lines are not portable across OSes; for cross-platform logic prefer proc.exec plus the fs/os helpers.

**Signature:** `proc\shell(line, [shell]) → ShellCommand` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L374)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `line` | `string` |  | |
| `shell` | `string` | yes | |

**Returns:** map[string]any

### which

Resolve cmd against PATH and return its absolute path. RAISES when the command is not found - wrap it in try/catch to check a tool is installed and emit a clear hint instead of a cryptic exec failure.

**Signature:** `proc\which(cmd) → string` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L188)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `cmd` | `string` |  | |

**Returns:** string

### withSlots

Reserve n slots from magus's concurrency budget for the duration of callback. Use when callback runs a command with its own internal parallelism (make -j, a test runner) that magus can't see, so the global budget is not oversubscribed.

**Signature:** `proc\withSlots(n, callback)` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L494)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `n` | `int` |  | |
| `callback` | [`Callback`](https://github.com/egladman/magus/blob/main/std/module.go#L17) |  | |

### stdinIsTerminal

Report whether standard input is a terminal (TTY) rather than a pipe, file, or /dev/null. Use it to fail fast with a clear message instead of blocking on a read of stdin that will never receive piped input.

**Signature:** `proc\stdinIsTerminal() → bool` · [source](https://github.com/egladman/magus/blob/main/std/os.go#L174)

**Returns:** bool

