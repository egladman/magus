---
title: Tips and tricks
description: Non-obvious ways to combine magus subcommands - status sidebars, --step debugging, watch loops, health probes, and recursive invocation.
tags: [tips, status, step, watch, repl, recursion, magus.cmd, magus status]
---

# Tips and tricks

Non-obvious ways to combine magus subcommands.

## Live pool snapshot in a multiplexer sidebar

`magus status` is a non-blocking, one-shot RPC snapshot: it returns immediately whether the daemon is running or not. Combine `--compact` (a single densely-packed line) with `--watch` to keep a tmux/screen sidebar pane current:

```sh
magus status --compact --watch=1s
```

Sample output:

```
daemon 3/8 busy · api:build(2.1s) · ui:test(0.5s) · 1 ws
```

When no daemon is running the line reads `daemon: off`, with no error and no hang. Drop `--compact` for the full grid view when you have a wider pane to spare.

## Step through a target to diagnose a flaky build

`magus run --step` pauses before every subprocess and lets you inspect state, skip commands, or open a REPL mid-run. Concurrency is forced to 1, so commands execute one at a time:

```sh
magus run build --step
magus affected build --step
```

See [`--step`](debugging.md#--step) for the full prompt reference.

## Re-run only affected projects on each save

Pipe `magus watch` into `magus affected --stdin` for a tight inner loop that re-runs only the projects touched by each edit:

```sh
magus watch | while IFS= read -r path; do
    echo "$path" | magus affected --stdin test
done
```

## One-shot daemon health probe

`magus status` exits 0 even when the daemon is down (the pool block reads `daemon: off`). Use it as a cheap, non-blocking reachability probe in scripts or CI health checks, with no risk of hanging on a network timeout:

```sh
magus status
magus status -o json   # machine-readable output
```

## Interactive debugging entry points

Two entry points into an interactive Buzz REPL, sharing one evaluator:

- **`magus repl`** - standalone shell with magusfile bindings preloaded.
- **`magus.pry()`** - `binding.pry`-style breakpoint that opens the same REPL mid-target with frame context (`.where`, `.locals`, `.up`/`.down`, `.step`, ...).

```buzz
export fun build(args: [str]) > void {
    os.exec("go", ["generate", "./..."]);
    magus.pry();   // execution pauses here; inspect or modify state
    os.exec("go", ["build", "./..."]);
}
```

`magus run build --step` pauses before every subprocess instead (concurrency forced to 1) so you can step, skip, or drop into a REPL command-by-command.

Full reference (meta-commands, pry stack navigation, `--step` keymap, multiline behavior) is in [debugging](debugging.md).

## Recursive invocation

Targets can call `magus` recursively. Child invocations forward work to the parent process over a local socket; concurrency limits are shared, so nested calls draw from the same budget instead of each grabbing their own slots.

```buzz
magus.cmd(["run", "build", "api"]);
```

`magus.cmd` is the in-magusfile entry point for invoking magus recursively. When a [daemon](daemon.md) is running, the call rides the existing socket connection instead of spawning a new process.
