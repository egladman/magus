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

A target dies with no failing command. In CI the job simply stops, often with

```text
The runner has received a shutdown signal.
```

Nothing failed. The machine ran out of memory and took magus with it, so magus
never reached the point where it prints a summary.

## What magus tells you

magus watches host memory for the life of an invocation and streams a warning the
moment headroom collapses. Streamed, not summarized, because a killed process
never gets to print a summary - only what already reached the log survives.

```text
[warn] memory headroom low: 265MB available of 15989MB total; a target here is
close to taking the machine down; running: .:test; buzz heap: 8402931 objects
(peak 8402931), most of it from coverage.buzz:88
```

Three facts, in the order you need them:

- **the machine is nearly out** - available against total
- **what was running** - the project and target, read from the registry that
  survives a `SIGKILL`
- **whether it was Buzz, and where** - the heap object count and the source
  position responsible for most of its growth

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

`.heap` sits alongside `.where`, `.locals` and `.globals` - see
[Debugging](debugging.md) for the rest of the pry surface. It answers the one
question a paused stack cannot: where you _are_ says nothing about what filled
memory getting there.

The peak is rebased per invocation, so under `magus server` a long-lived daemon
reports what the current run did rather than the worst of everything it has ever
served.

## The pattern that costs gigabytes

This is the one worth recognizing on sight:

```buzz
var kept = "";
foreach (line in profile.split("\n")) {
    kept = kept + line + "\n";
}
```

Every `+` builds a whole new string, and every intermediate is pinned. Over a
30,803-line file that measured a **13.1GB peak to produce 2.1MB of output**, which
is enough to kill a 16GB CI runner.

Collect and join once:

```buzz
var kept = mut [<str>];
foreach (line in profile.split("\n")) {
    kept.append(line);
}
final out = kept.join("\n");
```

Same output, **29.5MB** peak. A list appends a reference per line; the
intermediates never exist.

The same shape appears whenever a value is rebuilt inside a loop:

| Instead of                                             | Write                         |
| ------------------------------------------------------ | ----------------------------- |
| `s = s + x` in a loop                                  | append to a list, `join` once |
| `while (s.indexOf(x) != null) { s = s.replace(x, y) }` | `s.split(x).join(y)`          |
| counting with `replace` in a loop                      | `s.split(x).len() - 1`        |

Note `str.replace` substitutes only the FIRST occurrence, which is why the
rescanning loop gets written in the first place.

## Scale is what makes it fatal

None of these patterns is wrong in itself. Building a ten-row table with `+` is
fine and always will be. The cost is the pattern multiplied by the input, so the
same three lines are harmless over a config file and fatal over a coverage
profile.

That is also why magus does not lint for it. A source scan for `x = x + …` across
this repository flags several hundred sites, nearly all of them loop counters and
small string building. No static check can see the input size, so magus measures
instead and speaks only when the measurement says something.

## Declaring what a target needs

A target that legitimately needs a lot of memory can say so, and magus will keep
peers off the machine while it runs:

```buzz
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
that alone exceeds the machine - for that, size the work itself:

```buzz
final procs = platform\memoryBytes() / (8 * 1024 * 1024 * 1024);
```

`platform\memoryBytes()` reports 0 when the host cannot be measured, so branch on
it rather than treating it as "no memory".
