---
title: Formatting, linting, and generated files
order: 9
description: Formatting and linting are different operations with different guarantees, and generated output is where conflating them breaks. Why a linter must never gate on generated files, why a formatter should cover them, and how to scope both from declared outputs.
tags:
  [
    generated,
    outputs,
    format,
    lint,
    drift,
    ignore-files,
    codegen,
  ]
---

# Formatting, linting, and generated files

## Formatting is not linting

These are different operations. Much tooling blurs them, and the blur is the source
of a whole family of avoidable problems.

|                          | Formatting                           | Linting                     |
| ------------------------ | ------------------------------------ | --------------------------- |
| What it computes         | a canonical form of the same program | judgments about the program |
| Output                   | bytes                                | findings                    |
| Can it disagree?         | no, once a normal form is chosen     | yes, that is the point      |
| Safe to apply unattended | yes                                  | no                          |
| Who acts on it           | nobody, it is mechanical             | a human, per finding        |

A formatter is a total function on syntax: feed it a valid file, get back the one
canonical spelling of that same program. There is no judgment in it and nothing to
review. A linter is the opposite: every finding is a claim that a human should decide
about, and a fix may change what the program means.

magus keeps them apart. Formatting is `format` (report by default, apply under the
`rw` charm); linting is `lint`. A spell exposes them as separate ops even when one
binary implements both.

**Take this seriously even when your tool does not.** A tool that reports "this file
is not formatted" as a lint finding has turned a mechanical fact into a judgment, and
a tool whose one `--fix` flag both reformats and rewrites semantics has made the safe
operation inseparable from the unsafe one. When your toolchain conflates them, split
them back apart at the op boundary: one op that formats, one that lints, so a target
can compose them independently.

## Generated files are where the conflation breaks

Ask what each should do with generated output and you get **opposite** answers. That
is the clearest demonstration that they are not the same operation, and a toolchain
that cannot express both answers separately cannot get this right.

### Linters: never gate on generated output

A lint finding asks a human to change a line. Nobody can act on that in a generated
file: the fix belongs in the generator, and the next regeneration overwrites whatever
the reader does. A gate that fires where its reader has no legal move is one people
learn to route around.

Exclude generated trees from linters, and say why at the exclusion:

```text
# Generated Markdown. Drift-gated by its own generators, not hand-authored, so a
# prose linter must not gate it - a rule fix belongs in the generator, not the output.
**/gen/
```

This is about actionability, not drift. It holds whether or not the generator happens
to emit lint-clean output.

### Formatters: cover generated output

The instinct is to exclude it here too. Do the opposite, provided one thing is true:

> **The generator emits formatter-normal output.**

When that holds, running the formatter over generated files is a no-op, so the two
cannot disagree and no drift is possible. When it does not, they oscillate: the
formatter rewrites the file, the next generate puts it back, and neither gate catches
it because each passes on its own.

Make it hold by putting the formatter **inside** the generator, as the last step
before the write, rather than running it over the output afterward. A Go generator
calls `format.Source`; the same idea applies to any language with a canonical form.

Once that is true, covering generated files is strictly better than excluding them,
because **the format check becomes the guarantee**. A generator that stops emitting
normal form fails the check by name, with the ordinary fix. Exclude it and you have
removed the only thing that would have told you.

## Generators must be byte-stable

**Same generator version, same inputs, identical bytes. No exceptions.**

The output must not vary with run count, machine, wall-clock time, filesystem walk
order, map iteration order, parallelism, locale, or absolute paths. If it does, the
generator is broken, and the fix is in the generator every time.

This is not a preference. Four things magus does are only sound if it holds:

- **Drift gating.** A gate compares regenerated output against what is committed. A
  generator that emits different bytes for the same inputs makes that gate report
  noise, and a gate that cries wolf is one people route around.
- **Caching.** A cache hit replays stored bytes instead of running the target. If
  rerunning would have produced something else, the cache is lying.
- **Merge resolution.** magus resolves a conflict in generated output by regenerating.
  That only converges if regeneration is deterministic.
- **Review.** A diff in a generated file should mean something changed upstream. If it
  can also mean nothing, every such diff has to be investigated to find out which.

What is _not_ a violation: output that changes because the generator changed, or
because a declared input changed. Those are the mechanism working. The invariant is
about a fixed generator and fixed inputs.

Usual causes, all fixable:

| Cause                      | Fix                                          |
| -------------------------- | -------------------------------------------- |
| map iteration order        | sort keys before emitting                    |
| timestamps                 | omit, or derive from a declared input        |
| absolute paths             | emit workspace-relative                      |
| filesystem walk order      | sort the walk results                        |
| parallel workers appending | collect, then sort                           |
| unpinned tool version      | pin it; the cache keys `tool:` lines already |
| random or sequential IDs   | derive from content                          |

## Check that your check can fail

That guarantee is worth exactly as much as the check's exit code, and one very common
formatter cannot fail at all:

```console
$ gofmt -l .        # lists every unformatted file
bad.go
$ echo $?
0
```

`gofmt -l` reports on **stdout** and exits 0. magus reads an op's verdict from its
exit code, so an unformatted file prints its own name and the run passes green. Most
modern check tools (`prettier --check`, `biome check`, `markdownlint`,
`ruff format --check`) signal through the exit code; older ones often do not.

Before you rely on any tool as a gate, run it against a deliberately broken file and
check `$?`. A check that cannot fail is worse than no check, because it looks like
coverage.

## Declared outputs answer "is this generated"

A target declares what it writes:

```buzz
export fun index_generate(ctx: magus\Context, args: [str]) > void {
    ctx.writesFiles("MAGUS.md");
    ...
}
```

That declaration is the single source of truth. `magus describe file` answers "is this
generated, and what produces it" from it, `magus clean --outputs` unions it across
every target, and magus writes the same globs into `.gitattributes` so a merge of
generated output resolves by regenerating instead of by hand.

Anything else needing the same answer should derive it from there. A second copy in an
ignore file is a list that rots silently, because nothing checks it against the tree.

## Adding a generated artifact

1. Declare it with `ctx.writesFiles`, so magus, `.gitattributes`, and `magus clean`
   all learn about it from one place.
2. Make the generator emit formatter-normal output.
3. Leave it in the formatter's scope. Do not add an ignore entry.
4. Add it to the linters' ignore, with a comment saying the fix belongs upstream.
5. Do not write a test that re-runs the generator and byte-compares. The `generate`
   target already drift-gates every generated file in the workspace; a per-artifact
   copy of that check is redundant and drifts from the real directive.

magus's own repository applies all of this, including one place where it deliberately
does not. See [Generated files in this repository](/development/contributing/) in the
contributor docs for the worked example.
