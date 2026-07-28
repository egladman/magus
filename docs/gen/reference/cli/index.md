---
title: The CLI in practice
description: The magus subcommands you actually reach for, grouped by the question you are asking, with the quirks that are not obvious from the help text.
tags:
  [
    cli,
    subcommands,
    ls,
    describe,
    run,
    affected,
    query,
    graph,
    doctor,
    json output,
  ]
---

# The CLI in practice

`magus` has around thirty subcommands. You reach for maybe six of them daily.
This page groups them by the question you are asking rather than
alphabetically, and covers the behavior that the per-command
[reference pages](../reference/manpage/magus/) do not.

Before anything else: **`-o json` works on every command that prints
structured output**, as do `-o yaml`, `-o name`, and
`-o template=<go-template>`. If you are scripting magus, or you are an agent
reading its output, reach for that first instead of parsing the human format.
The human format is allowed to change; the JSON shape is the contract.

## What is in this workspace?

`magus ls` enumerates the projects magus discovered, with what each declares:

```
workspace: /Users/you/repo (8 projects)

project: magus
  dir:  /Users/you/repo
  spell: magusfile
  sources: [magusfile.buzz **/*.go go.mod go.sum ...]
  depends_on: [libs/gopherbuzz]
```

`sources` and `depends_on` are the two lines worth reading. They are what the
cache key is derived from and what the affected set walks, so a target
rebuilding when you did not expect it is usually explained here.

`magus describe <thing>` does two jobs in one command, which is its main
quirk: it **defines** the concept, then **lists** every instance.

```
$ magus describe targets
definition: A target is a named operation (e.g. build, test, lint) declared as
an exported function in a project's magusfile ...

targets (44):
  ci  [canonical - affected/pipeline anchor; composed in the magusfile]
  biome-check  [spell: ts]
  build  [custom - projects: console]
```

The bracket suffix tells you where each came from - a spell, a custom
magusfile function, or the canonical set. `describe` accepts `tools`,
`targets`, `projects`, `workspaces`, and `mcp-tools`.

`magus where` fuzzy-matches a project and prints its absolute path, bare and
alone, so it composes:

```sh
cd "$(magus where console)"
```

## Run something

`magus run <target> [projects...]` is the workhorse. With no projects it runs
everywhere the target is defined; naming projects narrows it.

`magus affected <target>` runs only what a VCS diff says changed. It is not a
filter over `run` - it walks the dependency closure, so a project you did not
touch is included when something it depends on moved.

One thing to know: **`affected ci` errors when no project in scope declares a
`ci` target.** That is deliberate. Ordinary targets fan out and skip projects
that lack them, but `ci` is the anchor the affected set keys off, so a missing
one would exit 0 having gated nothing.

For a tight loop, pipe the watcher into it:

```sh
magus watch | magus affected --stdin build
```

`magus x` is the interactive shorthand - pick a project and target from a
picker. It requires a TTY and will not work in a pipeline.

## Why did that happen?

Every run prints an output reference, on success and failure alike:

```
[pass] docs (ran, 1m36s)
  magus run generate:rw docs
out3a777178
summary: 2 cached, 5 ran, 0 failed (1m46s)
```

`magus query output out3a777178` replays exactly what that run printed. A
failure adds the reproduce command and an inspect hint:

```
[fail] docs generate:rw (ran, 50s)
  cause: magus generate docs: 1 spell(s) failed
  output: out462efb79
  inspect: magus query output out462efb79
  reproduce: magus run generate:rw docs
```

References are **local to your machine**. One pasted from CI or a teammate
resolves to nothing, which is why the inspect hint disappears when magus
detects CI. See [output references](../concepts/cache/output-refs.md).

`magus tail` streams the most recent cached log for the project in your current
directory - useful when a run has already scrolled past.

For structural questions, the knowledge graph commands answer different shapes
of "why":

- `magus query <term>` - search, and show a node's neighborhood
- `magus explain <node>` - one node: its edges, provenance, blast radius
- `magus path <a> <b>` - the shortest path between two nodes
- `magus refs <symbol>` - where an ingested code symbol is defined and used
- `magus graph stats` - where the workspace concentrates and where it is neglected

`graph stats` is the one to run when you have inherited a repository:

```
graph: 2137 nodes, 4665 edges
connectivity: 2 component(s), largest holds 2136, 0 isolated node(s)

god nodes (most connected):
  DEGREE    IN   OUT  KIND         LABEL
      77    77     0  import       std
      61     2    59  target       content-generate
```

A high-degree node is a structural risk: everything depends on it, so changing
it touches everything. Isolated nodes are the opposite problem - something the
builder never linked up.

`magus insight` answers the same questions from VCS history instead of
structure: hotspots, change affinity, ownership, trend, volatility.

## Is my setup sane?

`magus doctor` validates the workspace and is the first thing to run when
something behaves strangely. `magus config` views and updates configuration;
`magus init` bootstraps `magus.yaml`, a starter magusfile, and the VCS merge
driver for generated outputs.

## Long-running processes

`magus server start` backgrounds the daemon, which keeps the knowledge graph
warm and serves MCP. Starting one when it is already running is a no-op that
still exits 0, so it chains safely in scripts. `magus server stop` exits
non-zero when it found nothing to stop.

`magus status` inspects the concurrency pool of a running parent magus - what
is executing, what is queued, and what is waiting on a slot.

## Verbosity

Every command honors the same output flags. Which one to reach for, and what
each actually prints, is covered in
[Logging and verbosity](../reference/logging.md). The short version: `-v` for
"why did this rebuild", `-vv` when you want to watch the build happen, and
`--silent` for unattended runs.
