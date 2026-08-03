---
title: Nx
description: Map an existing Nx workspace into magus with a workspace provider - nx keeps running the work, magus gets the project graph, the affected set, and the knowledge graph - without committing anything to the repo.
tags: [nx, workspace-provider, monorepo, typescript, adoption, integration]
---

# Nx

An Nx repo already has a project model: `nx.json`, a `project.json` (or an
inferred target set) per project, and a dependency graph Nx computes from the
source. A [workspace provider](../../concepts/workspace-providers.md) lets magus
adopt that model instead of asking the repo to carry a magusfile per project.

Nx keeps doing the work. Every target magus runs shells out to Nx, so nothing gets
faster. What you gain is magus's view of the repo: a project graph you can query,
an affected set you can compare against Nx's, and the ownership, churn and
coverage that magus derives from running the work.

## Nothing is committed to the repo

The shim is three untracked files at the repo root plus magus's cache directory,
and `.git/info/exclude` - which is per-clone and never committed - keeps them out
of `git status`:

```text
magusfile.buzz     wires the provider; the root project
spells/nx.buzz     the provider spell below
.magus/            magus's cache (relocate with MAGUS_CACHE_DIR to keep the tree cleaner)
```

```sh
cat >> .git/info/exclude <<'EOF'
/magusfile.buzz
/spells/
/.magus/
EOF
```

A teammate who never runs magus sees an unchanged repo. Nothing lands inside a
project directory, which matters: Nx's default `inputs` include
`{projectRoot}/**/*` and Nx hashes untracked files, so a file dropped into every
project directory would change every task hash on your machine and stop you
hitting the shared cache.

## The magusfile

```buzz title="magusfile.buzz"
import "magus";
import "spells/nx";

magus\workspace.provider(nx);

// The root project. Nothing here runs Nx: the provider's projects carry that.
export fun preflight(ctx: magus\Context, args: [str]) > void {}
```

## The spell

```buzz title="spells/nx.buzz"
// Nx workspace provider: reports the Nx project graph as magus projects, and runs
// each target by shelling out to `nx run <project>:<target>`.
//
// Op keys are magus LIFECYCLE names (build/test/lint) instead of the tool's own
// command, reversing the usual rule (see docs/concepts/spells.md). A provided
// project has no magusfile, so a target name reaches it only when a bound spell
// exposes an op of that name.
import "magus/spell";
import "os";
import "json";

export fun mgs_getName() > str { return "nx"; }
export fun mgs_getLanguage() > str { return "typescript"; }

// For a provider spell this is read at WORKSPACE scope: the files that decide what
// the projects ARE. It is also what invalidates the provider cache.
export fun mgs_listRequiredGlobs() > [Path] {
    return [Path{value = "nx.json"}, Path{value = "**/project.json"}, Path{value = "package.json"}];
}

export fun mgs_listIgnoreDirs() > [Path] {
    return [Path{value = "node_modules", isDir = true}, Path{value = "dist", isDir = true}];
}

// An Nx upgrade changes what the tool does, so it belongs in every cache key.
export fun mgs_getVersionCommand() > [str] { return ["npx", "nx", "--version"]; }

// The argv names no project: an op is resolved ONCE, before any project is
// selected, so target.projectPath is not available here. What identifies the
// project is the CWD - magus runs an op in the project's own directory, and
// `nx build` there resolves to that project.
fun build(target: Target) > Command { return Command{bin = "npx", args = ["nx", "build"]}; }
fun test(target: Target) > Command  { return Command{bin = "npx", args = ["nx", "test"]}; }
fun lint(target: Target) > Command  { return Command{bin = "npx", args = ["nx", "lint"]}; }

export fun mgs_listTargets() > any {
    return {"build": build, "test": test, "lint": lint};
}

// list_projects is the workspace-provider contract magus invokes by name.
export fun list_projects(target: Target, cb: fun(any)) > [Project] {
    final io = {<str: any>};
    cb(io);
    final r = (io["root"] as? str) ?? "";

    final listed = os\exec("npx", args: ["nx", "show", "projects", "--json"], dir: r);
    final names = json\parse(listed.stdout);

    var projects = [<Project>];
    foreach (name in names as [str]) {
        final shown = os\exec("npx", args: ["nx", "show", "project", name, "--json"], dir: r);
        final detail = json\parse(shown.stdout) as? {str: any};
        projects.append(Project{
            path   = ((detail?["root"]) as? str) ?? "",
            name   = name,
            spells = ["nx", "typescript"],
            // sources are PROJECT-relative: "**/*.ts", never "libs/foo/**/*.ts".
            sources = ["**/*"],
        });
    }
    return projects;
}
```

That is the smallest version that works. What it leaves out, in the order worth
adding:

1. **Dependencies.** `nx graph --file=<path>` writes the project graph, whose
   `dependencies` map gives each project's in-workspace upstreams. Feed them to
   `depends_on` and magus's affected set starts matching Nx's.
2. **Per-target inputs and outputs.** Nx 22.7+ has
   `nx show target inputs <p>:<t>` and `nx show target outputs <p>:<t>`, which
   report Nx's own resolved answer. Below that, `nx show project --json` carries
   `inputs`/`outputs` per target, with `{projectRoot}`/`{workspaceRoot}` tokens
   and `namedInputs` references for the spell to expand. Re-anchor both to the
   project directory before returning them.
3. **One call instead of N.** `nx show project` per project is a subprocess per
   project. `nx graph --file` gets everything in one shot.

## Two things to know about the mapping

**The op relies on Nx inferring the project from the working directory.** magus
runs an op in the project's own directory and an op's argv is fixed before any
project is chosen, so `nx build` in `libs/foo` is what stands in for
`nx run libs/foo:build`. That inference is Nx behaviour, not magus's: verify it
on your repo before trusting the mapping, and if a target of yours does not
infer, have the op run a small wrapper script that resolves the project from
`$PWD` and calls `nx run` itself.

**Outputs that leave the project directory cannot be declared.** Nx's default is
`dist/{projectRoot}`, which sits at the workspace root, while a `Project`'s
`outputs` are project-relative. Leave them undeclared rather than guessing. magus
then replays nothing for those targets, which beats replaying the wrong thing.

**Nx runs dependent tasks itself.** `nx run p:build` already builds what
`dependsOn: ["^build"]` names, so magus may schedule an upstream target that Nx
then replays from its own cache. Correct, and slower. Passing
`--excludeTaskDependencies` hands ordering to magus instead. Try that once the
affected sets agree, not before.

## What to do with it

```sh
magus ls                                   # the Nx project set, as magus projects
magus query kind:project                   # ... in the knowledge graph
magus explain project:libs/foo             # its edges and blast radius
magus affected --plan --base=main          # compare against nx show projects --affected
magus insight hotspots                     # churn x complexity, which Nx does not answer
magus refs <symbol>                        # cross-project symbol references (needs scip-typescript)
```

Start with the affected-set comparison. Run both over the same real commits and
score where they disagree. Agreement is evidence the mapping is faithful; a
disagreement is either a bug in the shim or a dependency Nx knows about that the
provider has not reported yet.

## See also

- [Workspace providers](../../concepts/workspace-providers.md): the mechanism
- [MGS1023](../../reference/codes/magusfile/MGS1023.md) and
  [MGS1024](../../reference/codes/magusfile/MGS1024.md): the two diagnostics a
  provider can raise
