# `os`

Process execution. os.exec runs a command directly (no shell); os.exec_sh runs a line through the shell. Both stream output live and return a result {stdout, stderr, code, ok}.

> **Naming convention:** Buzz reaches modules off the `import "magus/extra"` aggregate in `camelCase` (`extra.os.someMethod`).

## Methods

### `exec`

Run cmd directly (no shell; args are never shell-interpolated). Output streams live and is captured. Returns {stdout, stderr, code, ok}; raises on non-zero exit unless opts.allow_failure is true. Optional dir runs cmd there (relative to the target's cwd). opts.stdin is fed to the process as standard input — pipe by passing a prior call's stdout.

**Signature:** `extra.os.exec(cmd, [args], [dir], [opts]) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `cmd` | `string` |  | |
| `args` | `[]string` | yes | |
| `dir` | `string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### `exec_sh`

Run line through a shell — for pipes, redirection, globs, and variable expansion. Default shell is /bin/sh (cmd on Windows); pass opts.shell (e.g. "bash") to override, resolved via PATH. A shell line is written in the platform shell's dialect, so sh and cmd lines are not portable across OSes — for cross-platform logic prefer os.exec plus the fs/os helpers. Same result and raise semantics as exec (opts.stdin and opts.allow_failure included); optional dir runs the shell there.

**Signature:** `extra.os.execSh(line, [dir], [opts]) → map[string]any`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `line` | `string` |  | |
| `dir` | `string` | yes | |
| `opts` | `map[string]any` | yes | |

**Returns:** map[string]any

### `with_env`

Set env vars for the duration of callback; restore after.

**Signature:** `extra.os.withEnv(env, callback)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `env` | `map[string]string` |  | |
| `callback` | `Callback` |  | |

### `with_slots`

Reserve n slots from magus's concurrency budget for the duration of callback. Use when callback runs a command with its own internal parallelism (make -j, a test runner) that magus can't see, so the global budget is not oversubscribed.

**Signature:** `extra.os.withSlots(n, callback)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `n` | `int` |  | |
| `callback` | `Callback` |  | |

### `platform`

Return the Docker/OCI platform triple: (os, arch, variant).

**Signature:** `extra.os.platform() → string, string, string`

**Returns:** string, string, string

### `exit`

Abort the current run with the given exit code — typically after logging an error. Does NOT call os.Exit (that would kill a shared daemon); it raises, ending the target, and the code becomes magus's process exit status.

**Signature:** `extra.os.exit(code)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `code` | `int` |  | |

### `sleep`

Pause for the given number of milliseconds (fractional allowed), matching Buzz's os.sleep. Cancellable: if the run is interrupted it returns early with the cancellation error rather than blocking.

**Signature:** `extra.os.sleep(ms)`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `ms` | `float64` |  | |

### `which`

Resolve cmd against PATH and return its absolute path, or "" if it is not found. Use it to check a tool is installed before running it (and emit a clear hint/error instead of a cryptic exec failure).

**Signature:** `extra.os.which(cmd) → string`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `cmd` | `string` |  | |

**Returns:** string

### `stdin_is_terminal`

Report whether standard input is a terminal (TTY) rather than a pipe, file, or /dev/null. Use it to fail fast with a clear message instead of blocking on a read of stdin that will never receive piped input.

**Signature:** `extra.os.stdinIsTerminal() → bool`

**Returns:** bool

