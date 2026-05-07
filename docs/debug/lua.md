# Debugging Lua Magusfiles

Magus magusfiles are written in [Teal](https://github.com/teal-language/tl), a typed dialect of Lua 5.1.

## Interactive debugging: `magus.pry()`

Call `magus.pry()` from any target function to suspend execution and drop into a REPL with the full module surface available:

```lua
-- Targets are exported global functions; the os module is required where used.
global function debug_build(_args: {string})
    local result = require("magus.extra.os").exec_sh("go build ./...")
    magus.pry()  -- inspect `result` interactively
end
```

Exit the REPL with `os.exit()` or Ctrl-D to resume (or abort) execution.

## Structured script logging: `log.*`

Use the `log` module to emit structured messages that participate in the magus log stream:

```lua
magus.info("starting build", {service = "api", env = "prod"})
magus.warn("cache miss", {reason = "no prior run"})
```

Output follows the `MAGUS_LOG_FORMAT` format (`pretty`, `text`, or `json`).

## Engine selection

Magus uses two Lua backends:

| Backend                      | When used                | Notes                                       |
| ---------------------------- | ------------------------ | ------------------------------------------- |
| **LuaJIT** (`luajit`)        | CGO available            | Faster; requires a C compiler at build time |
| **Gopher-lua** (`gopherlua`) | No CGO (`CGO_ENABLED=0`) | Pure Go; slower startup                     |

The active backend is shown in `magus doctor`. Both backends run identical Impl functions — if a test passes on one, it passes on both.

## Common error shapes

```
magusfile: unknown target "build"
```

Target name mismatch. Names are lowercased; check for typos or camelCase.

```
os.exec go: exit 1
```

A command returned non-zero. `os.exec` raises on a non-zero exit; pass `{allow_failure = true}` (e.g. `os.exec("go", {...}, nil, {allow_failure = true})`) to get the `ExecResult` back and inspect `.code` instead of raising.

```
magus.dispatch: no targets match ["*-build"]
```

No registered target name ends in `-build`. Check `magus list` for registered names.
