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

The shim is four untracked files at the repo root plus magus's cache directory,
and `.git/info/exclude` - which is per-clone and never committed - keeps them out
of `git status`:

```text
magusfile.buzz     wires the provider; the root project
spells/nx.buzz     the provider spell below
magus.yaml         sandbox env passthrough for NX_*/NODE_*/npm_config_* (see below)
.magus/            magus's cache (relocate with MAGUS_CACHE_DIR to keep the tree cleaner)
```

```sh
cat >> .git/info/exclude <<'EOF'
/magusfile.buzz
/spells/
/magus.yaml
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
// Op keys are magus LIFECYCLE names (build/test/lint/ci) instead of the tool's own
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

// No mgs_getVersionCommand: package.json and the lockfile are already declared
// inputs, so an nx upgrade already misses every cache entry. A version probe
// would only add a node startup per project per run on top of that.

// The argv names no project: an op is resolved ONCE, before any project is
// selected, so target.projectPath is not available here. What identifies the
// project is the CWD - magus runs an op in the project's own directory, and
// `nx build` there resolves to that project.
//
// Builds the project nx infers from the working directory.
fun build(target: Target) > Command { return Command{bin = "npx", args = ["--no-install", "nx", "build"]}; }
// Tests the project nx infers from the working directory.
fun test(target: Target) > Command  { return Command{bin = "npx", args = ["--no-install", "nx", "test"]}; }
// Lints the project nx infers from the working directory.
fun lint(target: Target) > Command  { return Command{bin = "npx", args = ["--no-install", "nx", "lint"]}; }
// Chains lint, test and build so a provided project satisfies the ci anchor
// magus affected ci looks for.
fun ci(target: Target) > Command {
    return Command{bin = "sh", args = ["-c", "npx --no-install nx lint && npx --no-install nx test && npx --no-install nx build"]};
}

export fun mgs_listTargets() > any {
    return {"build": build, "test": test, "lint": lint, "ci": ci};
}

// list_projects is the workspace-provider contract magus invokes by name.
export fun list_projects(target: Target, cb: fun(any)) > [Project] {
    final io = {<str: any>};
    cb(io);
    final r = (io["root"] as? str) ?? "";

    var projects = [<Project>];
    // Belt and suspenders around the two exec calls below: NX_NO_CLOUD skips the
    // cloud-onboarding prompt, NX_TUI and NX_INTERACTIVE keep nx from trying to
    // draw a terminal UI while magus captures its output.
    os\withEnv({"NX_NO_CLOUD": "true", "NX_TUI": "false", "NX_INTERACTIVE": "false"}, fun() > void {
        final listed = os\exec("npx", args: ["--no-install", "nx", "show", "projects", "--json"], dir: r);
        final names = json\parse(listed.stdout);

        foreach (name in names as [str]) {
            final shown = os\exec("npx", args: ["--no-install", "nx", "show", "project", name, "--json"], dir: r);
            final detail = json\parse(shown.stdout) as? {str: any};
            projects.append(Project{
                path   = ((detail?["root"]) as? str) ?? "",
                name   = name,
                spells = ["nx", "typescript"],
                // sources are PROJECT-relative: "**/*.ts", never "libs/foo/**/*.ts".
                sources = ["**/*"],
            });
        }
    });
    return projects;
}
```

`ci` chains every target the base spell already exposes. Trim the chain to
what every project in the repo actually declares - nx errors on a project
that lacks one of the chained targets - or, if flavors diverge, split `ci`
across separate provider spells, one per flavor.

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

## Pinning the nx that runs

npm 7+'s `npx` prefers the workspace-local `nx` over anything global. But when
`nx` is not installed and stdin is not a TTY (or `CI` is set), `npx` assumes
`--yes` and silently downloads the latest `nx` from the registry and runs
that instead - the wrong version, none of the workspace's plugins.
`--no-install` turns that into a loud failure: `npx` errors instead of
guessing, which is why every `npx nx` call in the spell above carries it.

A globally installed `nx` delegates to the workspace-local version the same
way a gradle wrapper delegates to the pinned gradle - so `nx` on PATH is also
fine when you control the machines that run it. Yarn PnP repos have no
`node_modules/.bin` for `npx` to resolve against, so `npx` cannot find the
workspace `nx` there at all: use `yarn nx` as the op's `bin` instead, in
every `Command`.

The mapping needs nx 16.3+. `nx show projects --json` and
`nx show project --json`, both load-bearing in `list_projects` above, landed
in that release.

## Provider env hygiene

The nx cloud-onboarding prompt is TTY-gated, but its non-interactive skip was
only fixed around nx 20.2, and the nx 21 TUI is interactive-only regardless
of TTY. `NX_NO_CLOUD`, `NX_TUI`, and `NX_INTERACTIVE` cover all three cases at
once - belt and suspenders costs nothing here, so `list_projects` sets them
unconditionally around its two `os\exec` calls.

A one-shot nx command also starts the nx daemon, and that daemon outlives the
magus invocation that started it: it self-terminates after 3 hours idle,
keeps its state under `.nx/workspace-data`, and opens its socket in the OS
temp dir. CI disables it automatically. None of that needs magus
configuration; it is nx behaving normally, not a shim concern.

## Sandbox env passthrough

When [the sandbox](../../concepts/sandbox.md) is enabled, magus rebuilds a
child process's environment from an allowlist - `HOME`, `USER`, `PATH`,
`LANG`, `LC_*`, `TZ`, `TERM`, and a few more. `NX_*`, `NODE_*`, and
`npm_config_*` are not on it, so none of them reach `nx` unless the workspace
passes them through - silently different behavior from running `nx` bare in
a shell, where those variables are simply inherited.

```yaml
sandbox:
  env:
    passthrough:
      - "NX_*"
      - "NODE_*"
      - "npm_config_*"
```

This `magus.yaml` block joins the untracked shim files listed
[above](#nothing-is-committed-to-the-repo); the `.git/info/exclude` snippet
there already covers it.
[MGS2003](../../reference/codes/sandbox/MGS2003.md) reports every dropped
variable, so a missing passthrough entry fails loud - a broken nx run with a
stripped-var notice in the log - rather than silently behaving differently.

## Two things to know about the mapping

**The op relies on Nx inferring the project from the working directory.** magus
runs an op in the project's own directory and an op's argv is fixed before any
project is chosen, so `nx build` in `libs/foo` is what stands in for
`nx run libs/foo:build`. That inference is Nx behavior, not magus's: verify it
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

## Secrets

A provided project has no magusfile body, so `magus\secret.read` is out of
reach for it - there is no target function to call it from. A `Command` can
still declare `secrets`, and magus injects the resolved values into that one
child process at spawn, redacted from every captured output:

```buzz
fun publish(target: Target) > Command {
    return Command{bin = "npx", args = ["--no-install", "nx", "release", "publish"],
                   secrets = {"NPM_TOKEN": "NPM_TOKEN"}};
}

export fun mgs_listTargets() > any {
    return {"build": build, "test": test, "lint": lint, "ci": ci, "publish": publish};
}
```

See [Secrets](../../concepts/secrets.md) for how references resolve and what
the redaction guarantee covers.

## See also

- [Workspace providers](../../concepts/workspace-providers.md): the mechanism
- [Secrets](../../concepts/secrets.md): how a provided project reaches a
  credential without a magusfile body
- [Sandbox model](../../concepts/sandbox.md): the env allowlist a provided
  project's ops run under
- [MGS1023](../../reference/codes/magusfile/MGS1023.md) and
  [MGS1024](../../reference/codes/magusfile/MGS1024.md): the two diagnostics a
  provider can raise
