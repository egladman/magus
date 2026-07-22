---
title: "MGS1008: target missing its context parameter"
description: Fires when an exported magusfile function is not a valid target because its first parameter is not a magus\Context. The signature is the contract magus reads statically to build the graph.
tags: [MGS1008, magusfile, targets, context, signature]
---

# MGS1008: target missing its context parameter

Every target is an exported magusfile function whose FIRST parameter is a
`magus\Context`. An exported function without it is rejected at load, because the
signature is the contract magus reads - statically, without running the body - to
build the dependency graph and cache footprint.

```text
[MGS1008] target "build" must receive a magus\Context as its first parameter:
change its signature to export fun build(ctx: magus\Context, args: [str])
  see: .../MGS1008.md
```

## Why

A target declares what it needs and what it touches through the context it is
handed: `ctx.needs(...)`, `ctx.inputs(...)`, `ctx.outputs(...)`, `ctx.has_charm(...)`.
Binding those to the received `ctx` - rather than a floating global - is what lets
magus read the graph off the source text, so `magus ls`, `magus affected`, and the
graph render never execute a target body. A function without the context parameter
cannot declare anything that way, so magus rejects it rather than dispatch it with
the wrong arguments.

Buzz qualifies a namespaced type with a backslash, so the type is spelled
`magus\Context` (not `magus.Context`, which is not valid Buzz type syntax).

## Resolution

Add `ctx: magus\Context` as the first parameter:

```buzz
// before - rejected
export fun build(args: [str]) > void { go["go-build"](); }

// after
export fun build(ctx: magus\Context, args: [str]) > void { go["go-build"](); }
```

Declare dependencies and footprint through the context:

```buzz
export fun ci(ctx: magus\Context, args: [str]) > void {
    ctx.needs(lint, build, test);
}
```

A parameterized target keeps its extra parameters after the context:
`export fun release_build(ctx: magus\Context, goos: str, goarch: str) > void`.

## What this is NOT

- **Not about the second parameter.** Only the first parameter is checked; the
  `args: [str]` is conventional and may be named or omitted.
- **Not a spell error.** Spell contract functions (`mgs_getName`,
  `mgs_listTargets`, op definitions) are not targets and are not subject to this.

## See also

- [targets.md](../../../concepts/targets.md): how targets declare dependencies and footprint.
