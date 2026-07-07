---
title: Getting started
description: A linear, CLI-first walkthrough from installing magus to running your first spell-composed target and a ci pipeline.
tags: [getting-started, install, init, magusfile, tutorial, cli]
---

# Getting started

This is the guided, command-line path through magus: install the binary, bootstrap a workspace, write your first magusfile, run a target, bind a spell, and compose a `ci` pipeline you can run against only the projects a change touched. Follow it top to bottom. Every command here is real; run each one as you read.

If you would rather try magus without installing anything, the [interactive playground](playground.html) runs the same engine in your browser.

## 1. Install magus

magus ships as a single self-contained binary. Follow the [Download guide](download.md) for your platform, PATH setup, and signature verification, then confirm the binary is on your PATH:

```sh
magus version
```

The [Download guide](download.md) also covers `magus self update` and shell completion. This page assumes `magus` resolves on your PATH from here on.

## 2. Bootstrap a workspace with `magus init`

From the root of your repository:

```sh
magus init
```

`magus init` bootstraps a magus workspace in the current directory. It:

- Writes **`magus.yaml`**, the workspace config. By default this goes to the global user config location (`$XDG_CONFIG_HOME/magus/`); pass `--local` to write it into the repo instead, which is what you want for checked-in, team-shared config.
- Stubs a starter **`magusfile.buzz`** in the repo root: a working file with every canonical stage already declared as a no-op, ready for you to fill in.
- Wires the **VCS merge driver** so `magus.yaml` and the magusfile merge cleanly. The VCS is taken from `--vcs` (`git` or `hg`), or chosen interactively when stdin is a terminal.

For a non-interactive run (for example in CI), pick the VCS explicitly and write the config into the repo:

```sh
magus init --local --vcs git
```

Other flags: `--force` overwrites an existing config file, and `--global` writes only the global config and skips the per-repo magusfile stub and merge driver.

## 3. Read your first magusfile

Open the `magusfile.buzz` that `magus init` created. It looks roughly like this: every exported function is a runnable target, and the current directory is already registered as a project on defaults.

```buzz
import "magus";
import "os";

// Each exported function is a runnable target. Leave a stage as a no-op
// until you wire it.
export fun preflight(args: [str]) > void {}
export fun generate(args: [str]) > void { magus.needs(magus.target.literal("preflight")); }
export fun format(args: [str]) > void { magus.needs(magus.target.literal("generate")); }
export fun lint(args: [str]) > void { magus.needs(magus.target.literal("format")); }
export fun build(args: [str]) > void { magus.needs(magus.target.literal("format")); os.exec("echo", ["Hello from magus"]); }
export fun test(args: [str]) > void { magus.needs(magus.target.literal("format")); }

// 'ci' is the conventional anchor that `magus affected ci` keys off.
export fun ci(args: [str]) > void {
    magus.needs(magus.target.literal("lint"), magus.target.literal("build"), magus.target.literal("test"));
}
```

Three ideas carry the whole model:

- **Targets are exported functions.** There is no registration call for a target: export a `fun`, and its name becomes a runnable target. See [targets.md](targets.md) for the full model and the CLI grammar.
- **`magus.needs` declares prerequisites.** `magus.needs(magus.target.literal("format"))` says "run `format` first." magus builds a DAG from these edges, runs shared prerequisites once, and parallelizes independent branches.
- **`ci` is the anchor.** It is an ordinary target you compose with `magus.needs`. magus does not hardcode its steps, but it is the target `magus affected` keys off, and it always runs read-only.

List what magus discovered, then run the starter `build`:

```sh
magus ls            # list every discovered project, its files, outputs, and deps
magus run build     # run the build target for the project under the cwd
```

`magus ls` prints every project it found; `magus run build` runs your `build` target (the starter one echoes a greeting after `format`). With no project argument, `magus run` selects the project containing the current directory.

## 4. Bind your first spell

The starter `build` shells out with `os.exec`. That is fine for a one-off, but real toolchains belong in a **spell**: a library of tool-native operations plus the cache metadata that toolchain needs. A spell runs nothing on its own; it contributes operations (`go-build`, `go-test`, `go-vet`, ...) that your targets compose, and it tells the cache which files are inputs and outputs. See [spells.md](spells.md), and [Spells vs Targets](spells.md#spells-vs-targets) for where the line falls.

Bind the built-in `go` spell by importing it and listing it in `magus.project`, then compose its ops into your targets. Op keys are the CLI command in kebab-case, so they are reached by subscript (`go["go-build"]`):

```buzz
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

// Each exported function is a runnable target; its body calls the spell's ops.
export fun build(args: [str]) > void {
    magus.needs(magus.target.literal("format"));
    go["go-build"]();
}

export fun format(args: [str]) > void { go["go-fmt"](); }

// go-vet, golangci-lint, and govulncheck are all static analysis, so they
// compose into the canonical `lint` target, not bespoke targets.
export fun lint(args: [str]) > void {
    go["go-vet"]();
    go["golangci-lint"]();
    go["govulncheck"]();
}

export fun test(args: [str]) > void {
    magus.needs(magus.target.literal("format"));
    go["go-test"]();
}
```

Now the canonical targets run the real toolchain:

```sh
magus run format    # gofmt -l . (check only)
magus run lint      # go vet, golangci-lint, govulncheck
magus run build     # go build
magus run test      # go test ./...
```

A note on read vs write: targets are read-only by default, so `magus run format` **checks** formatting rather than rewriting it. To mutate in place, attach the built-in `rw` charm:

```sh
magus run format:rw   # gofmt -w . (rewrite files)
magus run lint:rw     # golangci-lint --fix (apply autofixes)
```

Charms are shared, composable execution modifiers attached after `:`; see [charms.md](charms.md). If you want autofix as the local default so you do not type `:rw` each time, set `default_charms: [rw]` in `magus.yaml` (charms.md covers the safeguards that keep CI read-only regardless).

## 5. Compose a `ci` target and run only what changed

`ci` is where the pieces come together. Compose it from your other targets with `magus.needs`; magus fans them out in parallel where the DAG allows and runs shared prerequisites once:

```buzz
export fun ci(args: [str]) > void {
    magus.needs(
        magus.target.literal("lint"),
        magus.target.literal("build"),
        magus.target.literal("test"),
    );
}
```

Run the whole pipeline locally:

```sh
magus run ci        # lint, build, test (read-only; rw is stripped from ci)
```

The payoff arrives with `magus affected`. Instead of running `ci` for every project, it runs `ci` only for the projects a version-control change touched, plus everything transitively downstream of them in the dependency graph:

```sh
magus affected ci             # run ci only for projects your changes touched
magus affected ci --base main # compare against a specific base ref
```

If you ever wonder why a project is in the affected set, ask:

```sh
magus affected ci --explain ./path/to/project
```

`magus affected ci` is the command your CI runs on every pull request. It keys off the `ci` anchor, computes the affected set from the VCS diff, and does the minimum work. For CI fan-out across runners, `magus affected --plan` emits a provider-neutral shard plan; see [`magus affected`](manpage/gen/magus-affected.md) for `--plan`, `--stdin`, and bisect.

## Recap

You now have the full loop:

1. `magus init` bootstrapped `magus.yaml`, a starter magusfile, and the merge driver.
2. Exported functions in `magusfile.buzz` became your targets.
3. `magus ls` and `magus run build` listed and ran them.
4. Binding the `go` spell let your targets compose real ops (`go-build`, `go-test`, `go-fmt`, ...).
5. A `ci` target composed with `magus.needs` runs the pipeline, and `magus affected ci` runs it only for what changed.

## Next steps

The [documentation index](documentation.md) is the map. From here, the core concepts:

- [targets.md](targets.md) - targets, the CLI grammar, and name resolution.
- [spells.md](spells.md) - spells, their ops, and the built-in spell catalog.
- [charms.md](charms.md) - `rw` and other execution modifiers attached with `:`.
- [workspace.md](workspace.md) - how magus discovers projects, and multi-project (monorepo) layout.
- [cache.md](cache.md) - the content-addressed cache that makes re-runs fast.
- [config.md](config.md) - every `magus.yaml` key, its `MAGUS_*` env var, and CLI flag.
- [sandbox.md](sandbox.md) - how spell subprocesses are confined to the workspace.

And the reference:

- [`magus init`](manpage/gen/magus-init.md), [`magus run`](manpage/gen/magus-run.md), [`magus ls`](manpage/gen/magus-ls.md), [`magus affected`](manpage/gen/magus-affected.md) - the commands in this guide.
- [playground.html](playground.html) - try any example live in the browser.
