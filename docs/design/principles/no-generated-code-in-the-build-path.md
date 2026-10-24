---
title: No generated code in the build path
order: 21
description: Scaffolding hands you a file and leaves; a generator that must run for the tool to work never leaves, so magus has none.
tags: [design, principle, codegen]
---

# No generated code in the build path

This is the position I am least willing to give up. A build tool should not put
code in your repository that you then have to keep in sync with the tool's
version. Dagger works hard to hide it and it is still there: authoring a module
writes `dagger.gen.go` and a tree of typed bindings next to your code, and the
docs recommend committing them. Autotools has worked this way for decades:
`configure.ac` becomes `configure` becomes a Makefile, generated code producing
generated code producing the thing that runs. It is impressive, and when it
breaks you need every layer in your head at once.

Marking generated files is right, and magus does it for its own output. The
objection is about who did the generating. In magus I declared that output, in
a target I wrote, and the tool does what I declared. A tool that writes a file
into my tree as a condition of working, and tells me to commit it, has put
itself between me and the build.

So the line: no generated code sits between you and running a build. A
magusfile is Buzz, and magus interprets it. Nothing compiles it, nothing binds
it, and no file lands in your tree that you keep synchronized with a tool
version. `magus init` scaffolds a magusfile once and never touches it again.
Scaffolding hands you a file and leaves. A generator that runs as a condition
of the tool working never leaves.

## Enforced by

- A magusfile is interpreted; there is no compile or bind step for it.
- `magus init` writes its files once; nothing else in magus writes into a
  workspace except a target the workspace declared.
- Every generated file in this repository is a declared output of a target, and
  the drift gate fails a hand edit of one.

## Good

A generated file this repository owns, declared by the target that writes it:

```buzz
export fun index_generate(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles(["MAGUS.md"]);
    ctx.readsFiles(["magusfile.buzz", "../magus.yaml", "../internal/render/target_graph.go",
                    "../internal/render/md/md.go", "../internal/hint/cli_command.go"]);
    index\render("docs");
}
```

## Bad

```text
$ dagger init --sdk=go
$ git status
  new file: dagger.gen.go
  new file: internal/dagger/dagger.gen.go
```

The tool wrote code you must commit and regenerate on every tool upgrade, and a
version mismatch between that code and the engine is now your build failure.

## See also

- [Sit next to the tools, never in front of them](sit-next-to-the-tools.md)
- [Behavior is code, facts are data](behavior-is-code-facts-are-data.md)
- [Generated files](../../concepts/generated-files.md)
