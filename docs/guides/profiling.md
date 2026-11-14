---
title: Profiling a magusfile
description: Find which line of a magusfile is filling memory, read the low-headroom warning magus streams before a machine dies, and fix the string-building pattern that costs gigabytes.
tags:
  [
    profiling,
    memory,
    performance,
    heap,
    pry,
    buzz,
    magusfile,
    troubleshooting,
    diagnostics,
    ci,
  ]
---

# Profiling a magusfile

Magusfiles are Buzz, and Buzz runs on a VM whose object heap **never shrinks**.
Every string, list, map and object a program allocates is appended to one table
and pinned for the life of the process. That is a deliberate trade for short
scripts, and it has one sharp edge: a magusfile can exhaust a machine without any
single value being large, and without a Go heap profile pointing anywhere useful.

This page is about finding that line.

## The symptom

A target dies with no failing command. In CI the job stops, often with

```text
The runner has received a shutdown signal.
```

Nothing failed. The machine ran out of memory and took magus with it, so magus
never reached the point where it prints a summary.

## What magus tells you

magus watches host memory for the life of an invocation and streams a warning the
moment headroom collapses. Streamed, not summarized, because a killed process
never gets to print a summary; only what already reached the log survives.

```text
[warn] memory headroom low: 265MB available of 15989MB total; a target here is
close to taking the machine down; running: .:test; buzz heap: 8402931 objects
(peak 8402931), most of it from coverage.buzz:88
```

Three facts, in the order you need them:

- The machine is nearly out of memory, shown as available against total.
- What was running is the project and target, read from the registry that
  survives a `SIGKILL`.
- Whether it was Buzz, and where, is the heap object count and the source
  position responsible for most of its growth.

That last clause is the one that separates "a subprocess ate the memory" from
"our own script did", which is otherwise a day of bisecting.

The warning is silent on a healthy run. It fires below an eighth of total memory
and then only on each further 250MB drop, so a tight build costs a handful of
lines rather than one every two seconds.

### Reading the heap figure

Objects, not bytes. The pathological shape is millions of _small_ strings, which a
byte reading makes look unremarkable. What diagnoses the problem is the shape of
the growth: an object count that climbs with the size of the input is quadratic in
memory whatever each object weighs.

The source position is the growing **loop**, not always the exact allocating
statement. magus samples the interpreter rather than instrumenting every
allocation, and in a tight loop the loop control runs as often as its body, so
either line may be reported. Both put you on the same few lines.

## Profiling interactively

Set a breakpoint and ask:

```buzz
import "magus";

export fun report(ctx: magus\Context, args: [str]) > void {
    final data = build();
    magus\pry();
}
```

Run the target, then at the prompt:

```text
pry> .heap
heap: 30660 objects live, 30660 peak this run
  growth by source position, largest first:
    coverage.buzz:88                         30352
  a position that climbs with input size is quadratic in memory;
  build into a list and join once rather than reassigning a string.
```

`.heap` sits alongside `.where`, `.locals` and `.globals`; see
[Debugging](debugging.md) for the rest of the pry commands. It answers the one
question a paused stack cannot: where you _are_ says nothing about what filled
memory getting there.

The peak is rebased per invocation, so under `magus server` a long-lived daemon
reports what the current run did rather than the worst of everything it has ever
served.

## The pattern that costs gigabytes

This is the one worth recognizing on sight:

```buzz
var kept = [<str>];
foreach (line in profile.split("\n")) {
    kept = kept + [line];
}
```

Every `+` builds a whole new list, and on the default nanbox build every
intermediate is pinned, so memory grows with the square of the line count.

Append to a `mut` list instead:

```buzz
final kept = mut [<str>];
foreach (line in profile.split("\n")) {
    kept.append(line);
}
```

A list appends a reference per line; the intermediates never exist.

A string extended at its end used to cost the same way: over a 30,803-line file,
`kept = kept + line + "\n"` measured a **13.1GB peak to produce 2.1MB of
output**. gopherbuzz now extends a string of 1024 bytes or more in one shared
buffer when it is the left end of a `+` chain or the first part of an
interpolation. That loop costs linear time and memory: at 8,000 lines its peak
fell from 2,158MiB to 68MiB, the same as collecting the lines in a list and
joining once. Text put before the string, as in `kept = line + kept`, still
copies the whole string every pass.

The same shape appears whenever a value is rebuilt inside a loop:

| Instead of                                             | Write                                  |
| ------------------------------------------------------ | -------------------------------------- |
| `xs = xs + [x]` in a loop                              | `xs.append(x)` on a `mut` list         |
| `m = m + {k: v}` in a loop                             | `m[k] = v` on a `mut` map              |
| `s = x + s` in a loop                                  | append to a list, `reverse().join("")` |
| `while (s.indexOf(x) != null) { s = s.replace(x, y) }` | `s.replace(x, with: y)`                |
| counting with `replace` in a loop                      | `s.split(x).len() - 1`                 |

`str.replace` substitutes every occurrence, so one call does what the
rescanning loop does, unless a replacement creates a new match.

## Scale is what makes it fatal

None of these patterns is wrong in itself. Building a ten-row table with `+` is
fine and stays fine. The cost is the pattern multiplied by the input, so the
same three lines are harmless over a config file and fatal over a coverage
profile.

The Buzz checker warns on the shape, as BZZ3002: a list or map that outlives one
pass of a loop and is rebuilt from itself with `+`, or a `str` rebuilt with text
before it. `s = s + x` and `s = "{s}{x}"` stay quiet. It reads types, so loop
counters stay quiet, and the warning never fails a run. No static check can see
the input size, so magus also measures, and speaks only when the measurement
says something.

## Declaring what a target needs

A target that legitimately needs a lot of memory can say so, and magus keeps
peers off the machine while it runs:

```buzz
import "magus";

magus\project({
    "targets": {
        "test": {"memory_mb": 10240},
    },
});
```

magus divides that by the host's memory-per-slot share and holds that many
concurrency slots, so the declaration throttles on a 16GB runner and barely
registers on a 64GB workstation without naming either machine. See
[configuration](../reference/config.md) for the rest of the target policy.

This bounds what runs _alongside_ the target. It cannot shrink a single target
that alone exceeds the machine; for that, size the work itself:

```buzz
final procs = platform\memoryBytes() / (8 * 1024 * 1024 * 1024);
```

`platform\memoryBytes()` reports 0 when the host cannot be measured, so branch on
it rather than treating it as "no memory".
