---
title: Workspace providers
description: Let a spell supply the workspace's project set by asking the tool that already owns it - nx, gradle, pnpm, cargo - so a repo needs no magusfile per project.
tags:
  [
    workspace-provider,
    projects,
    discovery,
    spells,
    extension,
    nx,
    monorepo,
    adoption,
  ]
---

# Workspace providers

A magus workspace normally learns its projects from the tree: a directory with a
magusfile is a project (see [workspace.md](workspace.md)). That rule assumes the
repo's project structure is magus's to declare.

Often it is not. An nx, gradle, pnpm, cargo or bazel repo already has a project
model, maintained by a team, in that tool's own files. Asking it to also carry a
magusfile per project is asking for a second source of truth that drifts.

A **workspace provider** is a spell that supplies the project set by asking the
tool that owns it. The magusfile wires one:

```buzz
import "magus";
import "spells/nx";

magus\workspace.provider(nx);
```

magus invokes the spell's `list_projects` contract once per workspace load and
folds what it returns into the workspace. From there those directories are
ordinary projects: `magus ls` lists them, `magus run test libs/foo` runs there,
and the affected set and the knowledge graph reach them like any other.

## One extension point, used three times

This is the third instance of one arrangement magus already uses twice:

| Wiring                         | Subsystem delegated  | The spell exports                          |
| ------------------------------ | -------------------- | ------------------------------------------ |
| `magus\cache.remote(github)`   | remote cache         | `enabled`, `get_artifact`, `put_artifact`  |
| `magus\ci.provider(github)`    | CI job-log structure | `group_start`, `group_end`, `annotate`, .. |
| `magus\workspace.provider(nx)` | the project set      | `list_projects`                            |

In all three the magusfile names a spell, the subsystem invokes contract
functions **by name**, and magus knows nothing about the foreign system. You
support a build tool magus has never heard of by writing a spell, without waiting
for a release.

You wire a provider per workspace, by hand. magus still infers nothing from tool
markers, so a stray `package.json` means what it always meant: nothing. The
workspace's own magusfile has to say that another tool owns its project set.

## The contract

A provider spell exports one function:

```buzz
import "magus/spell";

export fun mgs_getName() > str { return "nx"; }

// The files that decide what the projects are. For a provider spell this
// declaration is read at WORKSPACE scope, and it is what invalidates the cache.
export fun mgs_listRequiredGlobs() > [Path] {
    return [Path{value = "nx.json"}, Path{value = "**/project.json"}];
}

// list_projects is the workspace-provider contract. magus invokes it by name.
export fun list_projects(target: Target, cb: fun(any)) > [Project] {
    final io = {<str: any>};
    cb(io);                       // the host writes {root} into io
    final root = io["root"] ?? "";

    // ... shell out to the foreign tool, then:
    return [
        Project{
            path       = "libs/foo",
            name       = "@acme/foo",
            spells     = ["nx", "typescript"],
            depends_on = ["libs/shared"],
            sources    = ["**/*.ts"],
            outputs    = ["dist/**"],
        },
    ];
}
```

`Project` comes from `magus/spell`, the module that carries the shapes a spell
writes (alongside `Command`, `Service`, `Charm`). Its fields match the
`magus\project({...})` options map one for one, so you configure a provided
project in the same words you would have written by hand.

| Field        | Meaning                                                               |
| ------------ | --------------------------------------------------------------------- |
| `path`       | the project's directory, **relative to the workspace root**; required |
| `name`       | the human label, when the tool's project name is not its directory    |
| `spells`     | spell NAMES to bind (a spell cannot hold another spell's handle)      |
| `depends_on` | upstream projects, resolved exactly as a magusfile's `depends_on` is  |
| `sources`    | input globs, **relative to the project directory**                    |
| `outputs`    | output globs, **relative to the project directory**                   |
| `exclusive`  | the project must not run alongside its peers                          |

Watch the two anchors. `path` is workspace-relative, because that is a
project's identity; `sources` and `outputs` are project-relative, because that is
what every other declaration in magus means. For a project at `libs/foo`,
`**/*.ts` is right and `libs/foo/**/*.ts` matches nothing.

`list_projects` is a contract function rather than an op: it does work in the VM
(it shells out and shapes the answer) and returns data instead of a `Command`. So
it carries no `mgs_` prefix, which is reserved for the pure, argument-less
declarations magus reads before it has selected anything to run.

### What a provider cannot say

`targets` (per-target `skip_cache`/`exclusive`/`slots`) and `watch_ignore` are
absent from `Project`. They are magus execution POLICY, which no foreign tool
knows. Declare them in the magusfile with the central form, which runs after the
fold and therefore composes:

```buzz
magus\project("libs/foo", { "targets": { "test": { "slots": 4 } } });
```

An output the foreign tool writes OUTSIDE the project directory (nx's
`dist/{projectRoot}` convention) has no project-relative spelling and cannot be
declared. Leave it undeclared rather than guessing.

## Running targets in a provided project

A provided project has no magusfile, so its targets come from its bound spells.
magus dispatches a target name to every spell bound to the project, and a spell op
whose key matches that name runs. So a provider spell names its ops after magus
lifecycle targets instead of after the tool's CLI command:

```buzz
fun build(target: Target) > Command {
    return Command{bin = "npx", args = ["nx", "build"]};
}
export fun mgs_listTargets() > any { return {"build": build, "test": test, "lint": lint}; }
```

This is the one place the [op-naming rule](spells.md#naming-operations) bends,
and a provider spell's doc comment should say so: an op key is normally the
tool's own command, but here it is what makes a magusfile-less project runnable.

**An op's argv cannot name the project.** An op is declarative data resolved
ONCE, before any project is selected - that is what lets magus charm-patch it,
hash it into a cache key, and print it under `magus describe` without running
anything. So `target.projectPath` is not available when the argv is built, and a
command like `nx run <project>:build` cannot be assembled there.

What identifies the project instead is the **working directory**: magus runs an
op in the project's own directory, and most tools infer the project from it
(`nx build` inside `libs/foo` is `nx run libs/foo:build`). A tool that cannot
infer it needs the project to reach the command another way - through the
environment, or through a wrapper script the argv names.

`magus affected ci` anchors on a target named `ci`. For a provided project, a
bound spell satisfies that anchor the same way it satisfies `build` or `test`:
by exporting a `ci` op. For a project with its own magusfile, the magusfile
stays the only place a `ci` target can live - a spell's `ci` op only reaches
projects that have none.

A provided project also has no magusfile body to call `magus\secret.read`
from; a `Command`'s [`secrets` field](secrets.md) is how it reaches a
credential instead.

## Precedence

1. **A magusfile wins.** A directory that declares itself keeps its own
   definition, and the provider's configuration for it is ignored with
   [MGS1024](../reference/codes/magusfile/MGS1024.md). This is what makes a
   gradual migration work: convert projects to real magusfiles one at a time.
2. **The first provider wired owns a path.** Several providers may be wired (an
   nx repo with a cargo workspace inside it); wiring order is the tiebreak.
3. **`magus\project(path, {...})` layers on top**, because the registry is
   applied after the fold.

A path magus cannot accept fails the load with
[MGS1023](../reference/codes/magusfile/MGS1023.md) instead of being skipped: a
dropped project is a target that no longer exists, with nothing on screen to say
why. The rules it must pass are in that page.

## Caching

A provider shells out to another tool, and it runs on the load path of every
magus command. So its answer is remembered under the cache directory and
re-derived only when something that decides the project set changes:

- the files the spell declares in `mgs_listRequiredGlobs`;
- that glob list itself;
- the workspace's own `spells/**/*.buzz` and magusfile sources, so editing the
  provider spell invalidates its own answer;
- the built-in spell registry's hash, which moves with the magus binary.

A provider whose globs are missing, malformed, or matching no files **cannot be
cached**. It runs every time and warns, because a fingerprint over nothing is a
constant, and a constant pins one answer forever.

`magus clean --cache` clears it, and `cache.immutable` suppresses the write.

## When a provider is the wrong answer

- **The repo is magus's to define.** Write magusfiles. They are more precise,
  since they support per-target footprints (`ctx.readsFiles`), and they need no
  foreign tool installed.
- **You want magus to be faster than the other tool.** A provider delegates
  execution to that tool; it inherits its speed.
- **The tree is not a working checkout.** An exported revision has no installed
  toolchain, so `magus graph diff --rev` skips providers and reports a provided
  project as added.

## See also

- [Workspace and projects](workspace.md): how discovery works without a provider
- [Spells](spells.md): the contract a provider spell is written against
- [CI providers](ci-providers.md) and [Remote caching](cache/remote.md): the two
  sibling extension points
