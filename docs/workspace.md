---
title: Workspace and projects
description: A workspace is the discovered root; a project is a directory whose magusfile registers it, and depends_on wires the two into an ordered, cache-aware graph.
tags: [workspace, projects, discovery, magusfile, depends-on, monorepo]
---

# Workspace and projects

A **workspace** is the whole tree magus operates on: a single root directory, its `magus.yaml`, and the set of projects discovered beneath it. A **project** is one directory inside that tree whose presence of a magusfile registers it, together with the targets it declares. Every target you run (see [targets.md](targets.md)) is addressed by a project `Path` plus an operation `Name`; the workspace is the space those paths live in.

The split is deliberate. The workspace is the unit of _discovery, caching, and affected-set computation_ - it is opened once and shared. A project is the unit of _work_ - it owns a magusfile, binds spells, and declares its dependencies. magus never operates outside the one workspace it discovered.

## Design intent

- **Files first.** A directory becomes a project because it _contains a magusfile_, not because you list it in a central manifest. Discovery reads the tree; there is no registry to keep in sync.
- **Convention over ceremony.** A bare magusfile with nothing but exported target functions is a complete project on defaults. The optional `magus.project({...})` call only layers extra policy on top.
- **One root, repo-relative paths.** Every project `Path` is stored relative to the workspace root. This keeps target identity portable across machines and lets the CLI, `depends_on`, and the cache all speak the same coordinate system.
- **Explicit dependencies.** Cross-project edges are declared, never inferred. `depends_on` is the single source of truth for ordering, the affected set, and cache-key propagation.

## What a workspace is

The workspace root is the nearest ancestor directory carrying a root marker. `FindRoot` walks up from the current directory and stops at the first directory that contains any of these, in priority order:

| Marker           | Why it roots a workspace                      |
| ---------------- | --------------------------------------------- |
| `magusfiles/`    | a split-magusfile directory                   |
| `magusfile.buzz` | a magusfile at the root                       |
| `magus.yaml`     | the workspace config file                     |
| `go.mod`         | the Go-module root, as a last-resort fallback |

magus markers precede `go.mod`, so an explicit `magus.yaml` or `magusfile.buzz` always wins over a stray module boundary. `magus.yaml` (workspace configuration - see [config.md](config.md)) lives at the root; it is optional, and its absence does not stop discovery once a root is found by another marker.

The root is **canonicalised at discovery** (symlinks resolved via `filepath.EvalSymlinks`). Every project path is then computed relative to that real path, and the sandbox enforces access against resolved paths (see [sandbox.md](sandbox.md) and [targets.md#symlinks](targets.md#symlinks)).

## What a project is

A project is a directory that carries a **declaration file**: `magusfile.buzz`, or a `magusfiles/*.buzz` file for the split-magusfile layout. Discovery registers the directory as a project keyed by its repo-relative path; the workspace root itself registers as the path `.`.

A project owns:

- **its targets** - the exported functions in its magusfile become the runnable operations (`build`, `test`, `lint`, ...); no registration call is needed (see [targets.md](targets.md)).
- **its bound spells** - the tool libraries whose ops the targets compose (see [spells.md](spells.md) and [operations.md](operations.md)).
- **its policy** - dependencies, outputs, watch-ignore patterns, and per-target execution flags, all layered on by an optional `magus.project({...})` call.

## Project discovery

`project.Discover` walks the workspace root once with `filepath.WalkDir` and registers every directory that `hasDeclaration` reports true. The rules:

- **A magusfile registers a project.** A directory with `magusfile.buzz` (or a matching `magusfiles/*.buzz`) becomes a project. Nothing else registers one: auto-detection from tool markers such as a stray `go.mod` or `package.json` has been retired. If you want a directory to be a project, give it a magusfile.
- **The root is the project `.`.** The workspace root, if it carries a magusfile, is the project whose path is `.`.
- **Well-known directories are pruned.** Discovery skips a fixed set of ignore directories at any depth and does not descend into them: `.git`, `.hg`, `.jj`, `.magus`, `.build`, `vendor`, `node_modules`, `target`, and `gen`. A magusfile buried inside one of these is invisible. (`gen` is treated as machine-written output, never a discoverable project.)
- **Symlinked directories are not followed.** `WalkDir` does not traverse symlinks, so a symlinked directory is silently skipped and never registered as a project.

Discovery is cached against directory mtimes, so a repeat open on an unchanged tree restores the project set without re-walking.

## The magusfile

A project's magusfile is `magusfile.buzz` (or the split `magusfiles/*.buzz` form). Its **mere presence registers the project on defaults** - a magusfile that only exports target functions is complete:

```buzz
import "magus";
import "spells/hello";          // ./spells/hello/spell.buzz

magus.project({ "spells": [hello] });

// Each exported function becomes a runnable target.
export fun build(args: [str]) > void { hello.build(); }
export fun test(args: [str]) > void {}

// 'ci' is the conventional anchor `magus affected ci` keys off.
export fun ci(args: [str]) > void {
    magus.needs(magus.target.literal("build"), magus.target.literal("test"));
}
```

### `magus.project({...})`: layering policy

`magus.project({...})` is **optional**. It does not create the project (the magusfile's presence already did that); it layers configuration onto it. The options map accepts:

| Key            | Effect                                                                                      |
| -------------- | ------------------------------------------------------------------------------------------- |
| `spells`       | binds spell handles to the project, contributing their ops, sources, and outputs            |
| `depends_on`   | declares upstream project paths this project depends on (repo-relative or project-relative) |
| `outputs`      | declares the project-relative file globs this project produces                              |
| `sources`      | declares additional project-relative file globs feeding the cache key and affected set, on top of whatever the project's spells already claim - for real inputs a spell doesn't know about (non-code assets, sibling schemas, docs a generator reads) |
| `exclusive`    | marks the project as must-not-run-alongside-peers in a batch                                |
| `watch_ignore` | appends `glob` / `regex` / `literal` patterns to the project's watch-ignore list            |
| `targets`      | a per-target policy table (see below)                                                       |

Unknown keys in either map (a typo like `depend_on`, or a per-target policy key
other than `skip_cache`/`exclusive`/`slots`) are a magusfile load error, not a
silently dropped option - the error names the offending key and suggests the
nearest known one.

The `targets` sub-map keys a target name to a policy table:

| Policy      | Effect                                                                       |
| ----------- | ---------------------------------------------------------------------------- |
| `skip_cache` | opts the target out of the cache; magus always runs it and never replays it  |
| `exclusive` | runs the target alone - no peer target runs concurrently while it does       |
| `slots`     | the target holds N concurrency slots while it runs, throttling parallel work |

```buzz
magus.project({
    "spells": [go],
    "depends_on": ["../shared"],
    "outputs": ["dist/**"],
    "watch_ignore": { "glob": ["**/*.snap"] },
    "targets": {
        "test": { "slots": 4 },
        "build": { "skip_cache": true },
    },
});
```

## depends_on: cross-project dependencies

`depends_on` declares that this project's work depends on one or more upstream projects. Paths resolve exactly like CLI project arguments (via `file.Resolve`): a bare path (`shared`) is repo-relative to the workspace root; a dot-relative path (`../shared`) is relative to the declaring project; absolute paths and paths that escape the root are rejected (see [targets.md#path-resolution-on-the-cli](targets.md#path-resolution-on-the-cli)).

Declared edges are unioned with any edges a bound spell contributes, then deduplicated. From there they drive three things:

- **Ordering.** The dependency graph (`depgraph.Build`) adds an edge `project -> dep` for every `depends_on` entry. Upstreams run before their dependents, and a cycle is a hard error.
- **The affected set.** When a change touches a project, magus computes the **reverse closure** over these edges (`g.ReverseClosure`), so every downstream dependent is also selected. A change in `shared` pulls in everything that depends on `shared`, transitively.
- **Cache keys.** A dependent's cache key folds in the resolved cache keys of its upstreams as `dep:` lines (see [cache.md#the-cache-key](cache.md#the-cache-key)). When an upstream's key changes, the new key flows into the dependent's key, so the dependent misses transitively. This is how a change ripples deterministically through the graph rather than by rerunning everything.

## Monorepo patterns

A workspace can hold many projects. The common layout is one magusfile per project directory, each declaring its own spells and dependencies:

```text
repo/                 # workspace root (magus.yaml, go.mod)
  magusfile.buzz      # project "."
  api/
    magusfile.buzz    # project "api"
  web/
    studio/
      magusfile.buzz  # project "web/studio"
  shared/
    magusfile.buzz    # project "shared", an upstream of api and web/studio
```

### The central (monorepo) form

`magus.project` also accepts an explicit path as its first argument. This is the rarer **central form**: one magusfile declares options for a discovered project at another workspace path.

```buzz
magus.project({ "spells": [go] });          // configures THIS project (path from context)
magus.project("api", { "depends_on": ["shared"] });  // configures the discovered "api" project
```

The explicit-path form configures a project that **discovery already found** - it does not create one. The path is relative to the workspace root, not to the declaring magusfile's directory. Passing the magusfile's own directory name here is the classic footgun; to configure the calling project, omit the path. An explicit path that matches no discovered project is a hard error that lists the known projects.

### Addressing projects on the CLI

Project selection is a **positional** argument to `magus run`, `magus list`, and `magus clean`, never embedded in the target token (see [targets.md#cli-grammar](targets.md#cli-grammar)):

| Input                        | Selects                                        |
| ---------------------------- | ---------------------------------------------- |
| bare (`api`, `web/studio`)   | the project at that workspace-relative path    |
| dot-relative (`./x`, `../x`) | resolved against the current working directory |
| `.`                          | the project containing the current directory   |
| empty (omitted) or `/`       | all projects (fan-out)                         |

Empty and `/` both fan out to every discovered project. A `ws:` prefix is **rejected**: magus tells you to use `/` for all projects instead. An unknown bare path is an error with a did-you-mean suggestion. Because paths are repo-relative, a bare `api` means the same project regardless of your current directory, while `../foo` behaves as a shell user expects.

## How this connects to affected and the cache

The workspace/project model is the substrate the affected engine and the cache build on:

- **Affected computation** attributes each changed file to the project that owns it, seeds the change, and takes the reverse closure over `depends_on` to select every dependent. `magus affected ci` runs only that set. See [operations.md](operations.md) for where affected sits, and the seed/claim mechanics in [cache.md](cache.md).
- **The cache** is content-addressed per target. A target's key includes its own inputs plus the `dep:` keys of its upstream projects, so cross-project dependencies invalidate transitively without rerunning unaffected work. This page does not restate the key format; see [cache.md](cache.md).

Together, discovery gives magus the set of projects, `depends_on` gives it the edges, and the cache gives it the memory - so a run touches only the minimum the change demands.

## Glossary

| Term                | Definition                                                                                                                  |
| ------------------- | --------------------------------------------------------------------------------------------------------------------------- |
| **Workspace**       | The discovered root, its `magus.yaml`, and the set of projects beneath it. The `types.Workspace` value, keyed by root path. |
| **Project**         | A directory registered by a magusfile; owns its targets, bound spells, and policy. The `types.Project` struct.              |
| **Root**            | The workspace root directory, found by `FindRoot` and canonicalised at discovery. Every project `Path` is relative to it.   |
| **Discovery**       | The single `WalkDir` pass (`project.Discover`) that registers a project per directory carrying a magusfile.                 |
| **magusfile**       | `magusfile.buzz` (or `magusfiles/*.buzz`); its presence registers a project. Exported functions become targets.             |
| **`magus.project`** | The optional call that layers policy (spells, `depends_on`, outputs, watch-ignore, per-target flags) onto a project.        |
| **depends_on**      | Declared upstream project paths. Drive ordering, the reverse-closure affected set, and `dep:` cache-key propagation.        |
| **Ignore dirs**     | The fixed directory names discovery prunes at any depth (`.git`, `vendor`, `node_modules`, `target`, `gen`, ...).           |

## See also

- [targets.md](targets.md): the addressable unit of work, project-path resolution, and the CLI grammar.
- [dependencies.md](dependencies.md): `depends_on` versus `magus.needs`, the fold between them, and how they feed the cache and the affected set.
- [operations.md](operations.md): the Spell to Operation to Target hierarchy and where affected computation sits.
- [spells.md](spells.md): the tool libraries a project binds and composes into targets.
- [cache.md](cache.md): the content-addressed cache key, including the `dep:` lines that propagate cross-project changes.
- [config.md](config.md): every `magus.yaml` key, its environment variable, and CLI flag.
