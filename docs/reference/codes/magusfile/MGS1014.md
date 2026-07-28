---
title: "MGS1014: cross-project output was never produced"
description: Fires when a target declares an output into another project's tree but the run produced no file matching it, so the cache would record an incomplete result.
tags: [MGS1014, magusfile, outputs, cross-project, cache, snapshot]
---

# MGS1014: cross-project output was never produced

A target declared an output into another project's tree, ran successfully, and
produced nothing matching that path.

```text
[MGS1014] target "build" declared an output into another project
("site/generated.txt") but produced no file matching it; check the path the
target actually writes
```

## Why

Nothing connects the path you **declare** to the path the target **writes**. They
are two independent strings:

```buzz
ctx.outputs(site.file("generated.txt"));   // the declaration
fs\writeFile("../site/generated.text", body);  // the write - note the typo
```

The declaration is what magus caches, orders, and replays. The write is what
actually happens. A mismatch between them is invisible at authoring time and,
without this check, invisible at run time too.

Ordinary outputs are lenient about this: a glob matching nothing is common and
usually harmless, so magus only complains when a target's *entire* output set
matched nothing. That leniency is what let this slip through. A target declaring
its own `dist/**` alongside a cross-project output passes the check on `dist/**`
alone, the manifest quietly omits the foreign file, and the run reports success.

A cross-project output cannot be treated that way, because another project's
build order hangs off it. The owning project is scheduled to run *after* this
target specifically so it sees the finished bytes. If the file was never written:

- The cache manifest omits it, so every later hit replays a **partial** output
  set into a tree this target does not own.
- The owning project builds against a file that is missing, or worse, stale from
  some earlier run - and reports success.

So a cross-project output is required rather than best-effort: it must match at
least one file, or the run fails before anything is cached.

## Resolution

Make the declared path and the written path agree. The declared glob is relative
to the **owning** project's root; the write is relative to the running target's
working directory, which is its own project directory. For a `producer` writing
into a sibling `site`, those are spelled differently for the same file:

```buzz
ctx.outputs(site.file("generated.txt"));       // relative to site/
fs\writeFile("../site/generated.txt", body);   // relative to producer/
```

Check, in order:

1. **A typo in either path.** This is the common case, and the two spellings
   differing by convention is what hides it.
2. **A conditional write.** If the body only writes the file on some branch, the
   declaration is unconditional but the write is not. Either write it every time
   or move the declaration onto a target that does.
3. **A different destination than you think.** Relative writes resolve against
   the target's project directory. Confirm with `magus run <target> -vv` and look
   at what the body actually touched.

## See also

- [MGS1011](MGS1011.md) - a cross-project output naming a project magus cannot use
- [MGS1013](MGS1013.md) - the glob half escaping its owner
- [Outputs and the cache](../../../concepts/cache.md)
