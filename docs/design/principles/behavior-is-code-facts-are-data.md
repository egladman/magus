---
title: Behavior is code, facts are data
order: 6
description: A target is a Buzz function you can read top to bottom; configuration holds only facts with no algorithm in them.
tags: [design, principle, configuration, buzz]
---

# Behavior is code, facts are data

Game engines face the choice a build tool faces: write a thing as code, or
declare it as data an engine interprets. Two questions settle it. First, does
the person who changes it have the code? A game's players usually do not, so
a recipe ships as data. A build's author always does: the magusfile sits in
the repository it builds, beside the code it compiles, so the usual case for
data never arises.

Second, would the data form narrow what the thing can do? Data-driving part
of a system restricts it to one behavior with different parameters. A game
that data-drives its armor too early ends with five hundred pieces that all
feel the same. A build tool that data-drives its targets ends with every
target an executor plus an options block, and every build the executor did
not anticipate needs a new executor. The patch for the lost expressivity is a
type field that picks an algorithm (`"executor": "@nx/vite:test"`, Skaffold's
`custom` builder handing off to a script), and options then layer from schema
defaults, workspace defaults, the target, a named configuration and the
command line until finding the value that won is its own investigation. The
data has grown into a language with no stack trace, no debugger, and no way
to read it top to bottom.

So magus splits along that line. A target is a Buzz function in the
magusfile; its dependencies are calls you can follow, and an unusual build is
an ordinary function rather than a plugin with a build step of its own.
`magus.yaml` holds facts with no algorithm in them: a remote spell's tag, a
remote cache's trusted keys, the charms every run applies. The stores stay
JSON Lines, which is data in the narrow sense: records magus writes and reads
back, never behavior a person authors.

This prevents the homogenized build, where every project looks alike because
the format left no room for one to differ, and the one that must differ gets a
fork of the plugin.

## Enforced by

- `magus.yaml`'s decoder rejects an unknown key (`decodeFile` in
  [`internal/config/load.go`](https://github.com/egladman/magus/blob/main/internal/config/load.go)), so the file cannot grow a dialect by accident.
- The [knobs rule](../../scope.md#the-knobs) answers each key added on purpose.
- A target exists only as a function a magusfile exports; there is no
  declarative target format to fall back to.

## Good

A target from [`docs/magusfile.buzz`](https://github.com/egladman/magus/blob/main/docs/magusfile.buzz): what it reads, what it writes and what it
runs, in order, in one place.

```buzz
export fun index_generate(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles(["MAGUS.md"]);
    ctx.readsFiles(["magusfile.buzz", "../magus.yaml", "../internal/render/target_graph.go",
                    "../internal/render/md/md.go", "../internal/hint/cli_command.go"]);
    index\render("docs");
}
```

## Bad

```json
{
  "targets": {
    "test": {
      "executor": "@nx/vite:test",
      "options": { "passWithNoTests": true },
      "configurations": { "ci": { "coverage": true } }
    }
  }
}
```

The executor string is a type field that picks an algorithm somewhere else.
What runs, and which of the schema default, `targetDefaults`, the target's
options, the named configuration or the command line set each value, cannot be
read from this file.

## See also

- [Coming from Nx](../../guides/migrating/from-nx.md)
- [Everything is a file](everything-is-a-file.md)
- [The knobs](../../scope.md#the-knobs)
