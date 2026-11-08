---
title: Connect two workspaces on one machine
description: "knowledge.workspaces in your user config links two local magus workspaces both ways, so a separate repository reuses another's lint config, analyzers, spells and Buzz modules straight from its live checkout. What it is for, what it is not for, how to set it up and remove it, and where it bites."
tags: [workspace, knowledge.workspaces, config, lint, golangci, buzz, symlink, guide]
---

# Connect two workspaces on one machine

`knowledge.workspaces` gives you the speed of a monorepo without one. Two repositories
stay separate in version control, and on your machine each one reads the other's working
tree as if it sat beside its own code. A separate repository reuses another workspace's
plumbing straight from that workspace's live checkout: its `.golangci.yml` and custom
analyzers, its spells, its Buzz modules, the Go spell's `spells/golang/golangci.buzz`.
A fix made in one tree shows up in the other on the next run. Neither repository moves in
lockstep with the other, and nobody cuts a release candidate of the magus binary to get a
fix across.

The first use is the buzzed editor, a Go repository of its own at `~/Repos/buzzed`. It
builds its lint from magus's analyzer sources, read from a magus checkout on the same
machine, instead of waiting for those analyzers to ship.

## What it is not for

- **Not a package manager.** Nothing is fetched, resolved or locked. The link points at a
  directory on disk, whatever is in it right now.
- **Not a way to pin or version a dependency.** There is no ref, no digest and no lock
  file. Two runs a day apart can read two different trees.
- **Not for CI.** CI has no user config, so it connects nothing. A target that reads
  through `.magus/workspaces/<name>` refuses there with
  [MGS1059](../reference/codes/magusfile/MGS1059.md), and every other target runs. A job
  can check the other repository out and write a user config itself, but then it builds
  against whatever that checkout holds, which is the unpinned build this page warns about.
  Ship anything CI must reproduce as a real dependency.

## How it works

- **One list, in your user config only.** Each entry is `{name, path}`. A repository's
  `magus.yaml` that sets the key refuses to load
  ([MGS1055](../reference/codes/magusfile/MGS1055.md)), so nothing about the connection is
  committed in either repository.
- **Both ways.** Every listed workspace sees every other. There is no direction to declare.
- **A symlink in each tree.** Every load in a listed workspace makes
  `.magus/workspaces/<name>`, a directory symlink to each other entry, in both trees, and
  prints one line per change. A load that changes nothing prints nothing.
- **Plain paths.** A target reads `.magus/workspaces/<name>/<path>` with
  `ctx.readsFiles`, and a magusfile imports a Buzz module by that path. The cache key names
  the path through the link and the bytes it read, never the other root, so the same
  bytes hit from any checkout. Each other root is read-only in the sandbox.
- **Only listed workspaces.** A workspace whose own root is not an entry sees nothing and
  makes nothing, so the list never reaches a checkout it does not name.

## Connect two workspaces

1. Find your user config file. magus reads the one file below for your platform, and no
   other:

   | Platform | User config file                                                                      |
   | -------- | ------------------------------------------------------------------------------------- |
   | macOS    | `~/Library/Application Support/magus/magus.yaml`                                      |
   | Linux    | `$XDG_CONFIG_HOME/magus/magus.yaml`, or `~/.config/magus/magus.yaml` when it is unset |
   | Windows  | `%AppData%\magus\magus.yaml`                                                          |

   On every platform an absolute `$XDG_CONFIG_HOME` wins. On macOS with it unset,
   `~/.config/magus/magus.yaml` is not read: an entry there connects nothing, and nothing
   says so.

2. Add an entry for each workspace, this one included. A name is `[a-z0-9-]+`, and a path
   is absolute or starts with `~`:

   ```yaml
   knowledge:
     workspaces:
       - name: magus
         path: ~/Repos/magus-main
       - name: buzzed
         path: ~/Repos/buzzed
   ```

3. Run any magus command in either workspace. The load makes both links:

   ```text
   ~/Repos/buzzed$ magus ls
   workspace magus: linked .magus/workspaces/magus -> ~/Repos/magus-main
   workspace magus: linked ~/Repos/magus-main/.magus/workspaces/buzzed -> ~/Repos/buzzed
   ```

4. Confirm the entries with `magus describe workspace`.

## Read through the link

A target names what it reads through the link like any other input, so a change in the
other tree misses its cache:

```buzz
ctx.readsFiles([".magus/workspaces/magus/libs/conventions/**", ".magus/workspaces/magus/libs/testpairing/**"]);
```

A magusfile imports another workspace's Buzz module by the same path. The Go spell's
`golangci.buzz` builds a custom golangci-lint from another workspace's
`.custom-gcl.yml` and runs it with a config you name. The build runs in a directory of
its own, so give it the `.custom-gcl.yml` as an absolute path:

```buzz
import "path";
import ".magus/workspaces/magus/spells/golang/golangci" as golangci;

golangci\buildFrom(path\abs(".magus/workspaces/magus/libs/testpairing/.custom-gcl.yml"), into: path\abs(".magus"));
golangci\lintWith(".magus", config: ".golangci.yml", dir: ".", args: []);
```

That is buzzed's lint: magus's analyzers, built from magus's live source, run with
buzzed's own `.golangci.yml` over buzzed's code.

## Remove a workspace

1. Delete its entry from your user config.
2. Run any magus command in a workspace that had it. The load removes the link in both
   trees and prints a line for each:

   ```text
   ~/Repos/buzzed$ magus ls
   workspace magus: removed .magus/workspaces/magus
   ```

Nothing else is left behind. Cache entries made through the link stay in the content
store and age out under the normal prune.

If you delete or unmount the other checkout and keep its entry, every load on the list
stops with [MGS1054](../reference/codes/magusfile/MGS1054.md), naming the line to delete.

## Pitfalls

- **The link follows whatever the other checkout has out.** A link to a checkout on a
  feature branch hands you that branch's lint config, analyzers and modules. A file the
  branch lacks fails your import, and a rule the branch adds fails your lint. Point the
  entry at a worktree that stays on main, such as one made with
  `git worktree add ~/Repos/magus-main main`, and update it when you want what merged.
- **A build is not reproducible from one repository.** A pass in buzzed depends on bytes
  in a tree buzzed does not record. The cache key holds them, so a rerun stays honest on
  your machine, but a clone of buzzed alone cannot rebuild what you saw.
- **Each workspace keeps its own `.golangci.yml`.** Each conventions analyzer reads the
  module it lints from the nearest `go.mod`, unless its settings name `module:`, and an
  explicit `module:` wins. magus's own `.golangci.yml` names
  `module: github.com/egladman/magus` for 13 of its analyzers and scopes settings to magus
  paths, so another workspace that ran it unchanged would judge its code as magus.
  Write your own config, enable the analyzers you want, and leave `module:` out so each
  reads your `go.mod`. A new analyzer in the other workspace reaches you only once your
  config enables it.
- **grep, ripgrep and `go ./...` skip the link.** They do not follow a directory symlink,
  and `.magus/` is ignored. Open the other tree by its path, or search it from its own
  checkout.

## Refusals

Each way the list or the trees can be wrong stops with a code naming what to change:

| Code                                               | When                                                                                      |
| -------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| [MGS1054](../reference/codes/magusfile/MGS1054.md) | an entry's path does not exist, is not a directory, or holds no magus workspace           |
| [MGS1055](../reference/codes/magusfile/MGS1055.md) | a repository's `magus.yaml` sets `knowledge.workspaces`                                   |
| [MGS1056](../reference/codes/magusfile/MGS1056.md) | an entry is a bare string, has another key, repeats a name, or has a relative path        |
| [MGS1057](../reference/codes/magusfile/MGS1057.md) | a listed workspace's VCS does not ignore `.magus/workspaces`                              |
| [MGS1058](../reference/codes/magusfile/MGS1058.md) | a regular file or directory sits where a link goes; magus deletes nothing it did not make |
| [MGS1059](../reference/codes/magusfile/MGS1059.md) | a target reads through `.magus/workspaces/<name>` and no such workspace is connected      |

## The author's own setup

> [!AUTHOR]
> I run buzzed and magus side by side on one Mac. My `~/Repos/magus` checkout is
> usually on a feature branch, so the `magus` entry points at a second worktree that
> stays on main, and buzzed lints against what has merged, not what I am in the middle
> of. My `~/Library/Application Support/magus/magus.yaml` holds two entries:
> `{name: magus, path: ~/Repos/magus-main}` and `{name: buzzed, path: ~/Repos/buzzed}`.
> When I fix an analyzer for buzzed, I make the fix on a magus branch, merge it, update
> the main worktree, and the next lint in buzzed picks it up with no release.

## What is not built yet

[ADR 0028](../decisions/0028-workspaces-see-each-other.md) designs more than this page
describes. Read verbs that cross by default, `affected` across workspaces, the guard
judging a write by the file's own workspace, and generated language joins such as a
`go.work` are proposed there and not built. Until then the union graph is
[`--global`](../concepts/knowledge.md#global-graph-across-workspaces).
