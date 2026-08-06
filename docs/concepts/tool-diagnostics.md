---
title: Tool diagnostics
description: A spell declares the convention its tool prints findings in - a standard like GNU, or an author-supplied pattern when no standard fits - so magus reads a failure as file, line and message instead of a wall of text a human has to parse by eye.
tags: [diagnostics, tool output, gnu, findings, spells, custom pattern, ci annotations, regex]
---

# Tool diagnostics

Errors are for humans. That is the same premise behind magus's own [coded diagnostics](../reference/diagnostics.md) (every `MGSxxxx` gets a cause, a resolution, and a queryable graph node) - it just points at a different wall of text. That page is about magus's own failures. This one is about the tool's: what `golangci-lint` or `cargo clippy` printed when your code was wrong, not when magus itself hit a snag.

A spell can declare `diagnostics` on any op: the convention that op's tool prints its findings in. When it does, magus reads that output as structured records - file, line, column, severity, message - instead of scraping it for keywords.

## The world without this

This is the baseline every built-in spell had until this landed, and it's still what an op gets if it declares nothing:

- **The failure excerpt is a heuristic.** `internal/cache`'s `failureExcerpt` scans the captured log for lines that *look* diagnostic - a regex matching words like `error`, `fail`, `undefined`, `mismatch` - and shows a window around each match, ranked and capped. It works because most tools print those words near the real cause. It's still a guess: it can surface the wrong line, or the right line with no idea which file it belongs to, because it never parsed structure. It pattern-matched prose.
- **CI annotations carry no location.** When a project fails, magus calls out to the CI provider with an [annotation](ci-providers.md) - but today that call sets `File` to the *project directory* and `Message` to a generic wrapped error string. Line, column and code are unset. A GitHub Actions run gets one annotation pointing at a directory, not the line that broke.

Both are triage aids, not answers - the real fix has always been `magus query output <ref>` and reading the whole log yourself. None of that is wrong; it's what you get from a build tool whose output was never meant to be parsed, only read. Tool diagnostics closes that gap for the tools that make it possible: ones that already print something structured, or that a spell author can teach magus to read.

## Declaring a format

`diagnostics` lives on the op's own `Command{}`, not on the tool's version-probe entry (`mgs_getTools`) - the op is the only place that knows which binary ran, since a wrapper (`pnpm exec eslint` vs. `pnpm exec tsc`) can run several different tools under one shared `bin`. The flag that produces the format belongs in the op's own `args`; **magus never rewrites argv**, so nothing about how a target runs changes silently between `magus describe` and what executes.

```buzz
// spells/docker/spell.buzz
fun hadolint(target: Target) > Command {
    return Command{bin = "hadolint", args = ["-f", "gnu", "Dockerfile"],
        diagnostics = DiagnosticFormat.gnu};
}
```

`hadolint -f gnu` now prints `hadolint:Dockerfile:1: DL3006 warning: msg`, and magus reads that as `{File: "Dockerfile", Line: 1, Severity: "warning", Code: "DL3006", Message: "msg"}` instead of a line it merely suspects is relevant.

## Standards, not shapes

`gnu` is a real, tool-independent format - the GNU Coding Standards diagnostic skeleton, `[program:]file:line[:column]: severity: message` - that magus implements **once**, tolerantly, and any tool opts into by printing it. hadolint spells it `-f gnu`; shellcheck spells the same skeleton `--format=gcc`; golangci-lint's default text output already matches it with no flag at all. The parser (`spells.ParseDiagnostics`) does not know or care which of those wrote a given line.

A registry of *per-tool* output shapes rots the moment a tool changes a flag name - golangci-lint v2 dropped the `--out-format` flag its own `gha` charm relied on, found by running it, not by reading its docs. A registry of *standards* doesn't, because the standard isn't magus's to keep in sync with anyone's CLI.

The parser is also more tolerant than the letter of the standard, on purpose, since real tools vary in the same handful of ways: buf's default output omits the space after the location (`file.proto:3:1:msg`, not `file.proto:3:1: msg`); Biome's `concise` reporter prefixes every line with a severity glyph (`! bad.js:1:10: msg`). Neither is a per-tool special case - both are one shared rule (an optional leading glyph, an optional space) that any tool decorating its output the same way gets for free.

## When no standard fits: a custom pattern

Not every tool has a standard to opt into. `tsc` is the motivating case: its only formatting flag is `--pretty`, which just toggles color, and its unflagged output is `file(line,col): error TSxxxx: msg` - parenthesized, not colon-delimited, and not GNU by any flag that exists. Forcing a "standard" onto a tool that doesn't have one just invents a shape that's tsc's shape with a shared name on it - the exact rot the standards-only design exists to avoid.

For exactly that case, `diagnostics = DiagnosticFormat.custom` pairs with `diagnosticPattern`: a Go-flavored regex, named capture groups instead of positional ones (`file` and `line` required; `col`, `severity`, `code`, `message` optional), declared once by whoever writes the spell:

```buzz
// spells/typescript/spell.buzz
fun tsc(target: Target) > Command {
    return Command{bin = "pnpm", args = ["exec", "tsc"], diagnostics = DiagnosticFormat.custom,
        diagnosticPattern = "^(?P<file>.+?)\\((?P<line>\\d+),(?P<col>\\d+)\\): (?P<severity>error|warning) (?P<code>TS\\d+): (?P<message>.*)$"};
}
```

Available to any spell, built-in or local, not just ones you write yourself. The convention (not an enforced rule) is that a built-in should still reach for a real standard first when one exists; `custom` is for when one genuinely doesn't.

## The nuances

**Structural mistakes fail at decode, not at run time.** A pattern that doesn't compile, or that compiles but never names `file`/`line`, is rejected the moment the workspace loads - the same place an unrecognized format or a malformed `key.upTo` already fails, naming the spell and op. You can't ship a custom pattern with a typo that only breaks the first time the op runs.

**A pattern that never matches the real tool can't be caught that way.** Decode has no output to validate against - none exists yet. A regex that compiles, names the right groups, and is just *wrong* (stale after a tool upgrade, a copy-paste mistake) degrades silently to zero extracted findings, which falls back to the pre-existing prose excerpt. That's a safe degradation, not a worse one, but it's still silent. `spells.LikelyMisconfigured` exists for this gap: it's true only when a run failed, a format was declared, the tool wrote something, and nothing was extracted - the one combination where a stale pattern is knowable. A clean pass with zero diagnostics is normal and doesn't trip it. The intended caller logs an operational `slog.Warn`, not a coded `MGSxxxx` diagnostic - a run-time signal, not a structural authoring bug the workspace should have refused to load with.

**A charm can silently invalidate a declared format, and there's no mechanism yet to track that.** `Diagnostics` is one static value per op. Some charms only add flags that don't touch output shape - golangci-lint's `debug`/`rw`, cargo-clippy has no charms at all - and those are safe. Others swap the reporter entirely: buf's and Biome's `gha` charms both exist to switch to a CI-annotation format for the runs where nobody's reading the raw log.

buf's case is still safe to declare, because its *unflagged* default is what `diagnostics` describes and `gha` only changes things when selected - the mismatch is real but narrow: a `buf-lint:gha` run reports `gnu` in its static declaration even though its output is GitHub Actions annotations. ESLint's and Biome's `check` op aren't safe at all: their `gha` charm inserts its own format flag at the same argv position a hand-written `diagnostics` declaration would need, so the two collide - exactly why neither is wired today. `custom`, `gnu`, and any future standard all share this limitation: the type system has no way yet to say "this format applies except under this charm." Fixing that is a real design question, not a bug to patch around, and it's what blocks the two most-used TypeScript tools in this repo. (ESLint separately has its own problem - its `unix` formatter was removed from ESLint core in recent majors, so the `gha` charm that names it is already broken, independent of any of this.)

**None of this keys the cache.** How a tool formats its findings doesn't change what it produced, so `Diagnostics` and `DiagnosticPattern` are read-only metadata for interpreting output after the fact - never part of the cache key, and never something `describe target --cache` needs to know about.

## Not to be confused with

[Diagnostics](../reference/diagnostics.md) is the unrelated system for magus's *own* coded errors (`MGSxxxx`): a magusfile that won't load, a sandbox violation, a drift gate that fired. This page is about the findings inside a tool's *own* output, once magus itself ran the tool successfully. The two meet at one seam: an `[MGSxxxx]` code is itself one line of output, and it happens to already look like a diagnostic - but nothing here treats it specially. It's just prose to this parser, the same as anything else magus didn't declare a format for.
