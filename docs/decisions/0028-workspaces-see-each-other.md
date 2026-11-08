---
title: "ADR 0028: workspaces see each other"
order: 28
description: "Two magus workspaces on one machine see each other while a person works across them. The existing user-config key `knowledge.workspaces`, given named entries, connects every listed workspace to every other, both ways, and nothing is committed in either repository. Each workspace gets a directory symlink, `.magus/workspaces/<name>`, to every other, so plain paths reach the other tree for a read, an editor, a Buzz import, a read pattern and a generated go.work. Discovery goes through magus verbs, never through the link: grep, ripgrep and `go ./...` skip it by design. Read verbs cross by default and label every row with its workspace, revision and freshness; each graph is built by its own workspace's binary and read in place, and a ranking weight leans toward the workspace the command runs in. A file is judged by the workspace that owns it. magus's lint rules become portable, so the other workspace adopts the config unchanged and makes exceptions with `//nolint` at the line. Removing an entry removes the links in both trees on the next load. A target that reaches a workspace that is not connected refuses. `--global` and the `<basename>//` qualifier are deleted."
tags: [adr, decision, workspace, config, cache, knowledge-graph, guard, lint]
status: proposed
date: 2026-11-06
---

# ADR 0028: workspaces see each other

## Context

### The goal

Eli, 2026-11-06: two repositories keep monorepo velocity without sharing a tree. B develops
against A's live checkout (its lint, magusfile targets and Buzz modules, conformance,
precedents, proofread and code references), so a fix made in A shows up in B on the next
run with no release cut, and B can patch A in place. They are coupled on purpose, and
neither needs the other in its repository. Two cases lead:

- A private repository working beside a public one.
- A repository you own needing a patch in one you don't.

Eli's brief: "we're essentially gluing this together. So any queries would be going cross
workspaces... We need to make it as explicit as possible." His reframe, later the same
day: connecting two workspaces is a development aid on one machine. Nothing is committed
to connect them, both sides see all of the other, the other side is visible on disk, and
no new subcommand is added.

The first instance is the buzzed editor (`libs/texteditor`) moving to its own repository,
whose Go is linted today by this repository's `.golangci.yml` and the analyzers in
libs/conventions and libs/testpairing.

In this record A is this repository, listed as `magus`, and B is the editor's, listed as
`buzzed`. Every rule below is symmetric: A sees B as B sees A.

### Decisions already made

Eli made each one.

| Date                          | Decision                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| ----------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| 2026-11-06, reframe           | Nothing is committed to connect workspaces. The existing `knowledge.workspaces` key, in the user config tier, with named entries `{name, path}`, connects every listed workspace to every other, both ways.                                                                                                                                                                                                                                                                                                                                                                                                              |
| 2026-11-06, reframe           | Each workspace is visible on disk under the other's `.magus/`, as `.magus/workspaces/<name>`, so plain paths work. No `@name/` syntax.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| 2026-11-06, reframe           | Existing verbs cross by default when the list is non-empty, rows are labeled by workspace name, and `--global` is deleted.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| 2026-11-06, reframe           | The silent defaults of `knowledge.workspaces` become loud refusals: an unopenable root (knowledge.go:165) and two roots with one basename (knowledge.go:194-197).                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| 2026-11-06, reframe           | A target that reaches through a missing workspace refuses, naming the workspace and the key that connects it; the workspace's own targets run.                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| 2026-11-06, reframe           | Removing a workspace is as simple as adding one: delete the entry, and the next load removes what it made in every tree, prints what it removed, and leaves nothing. Deleting the other checkout while its entry stays is a loud refusal naming the line to delete.                                                                                                                                                                                                                                                                                                                                                      |
| 2026-11-06, reframe           | Invent no terminology: workspace and `knowledge.workspaces` are the words.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| 2026-11-06, rounds 3 and 5    | The file's own workspace judges a write (its guard, leases and lint); the session crosses freely; the guard loads both policies. B's rule of a name wins on B's files, and a denial names whose rule fired.                                                                                                                                                                                                                                                                                                                                                                                                              |
| 2026-11-06, round 5           | A narrowing that matches nothing fails the run, naming it and where it is written.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| 2026-11-06, round 5           | affected crosses by default and says why (`magus changed: ...`).                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| 2026-11-06, round 5           | Rejected as the lint narrowing's home: `.golangci.use.yml`, a block in `.golangci.yml`, a per-entry block in magus.yaml.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| 2026-11-06, round 6           | `affected ci` lists the other workspace's affected targets and never runs them. The weight defaults to 0.7 until an eval. Two checkouts of one repository may both be listed, each named. A committed magusfile naming `.magus/workspaces/<name>` refuses at run time where it is not connected. The file is renamed to this one.                                                                                                                                                                                                                                                                                        |
| 2026-11-06, round 7           | Reverses round 6's lint answer (lint adapted by arguments to the adopting call): magus's rules become portable at the source. Each conventions plugin reads the module of the tree it lints; magus-only path rules match only in magus. buzzed takes magus's config unchanged; a new magus rule reaches buzzed with no buzzed change; exceptions are `//nolint:<rule> // <reason>` at the line.                                                                                                                                                                                                                          |
| 2026-11-06, round 8           | Discovery goes through magus verbs; the link serves paths (a read, an editor, an import, `go work use`), not search. Agents' grep, rg and `go ./...` skip it, which is accepted, and the guard steers a cross-workspace grep to `magus query`. A Go join is a generated go.work under `.magus/gen/`, with `GOWORK` set per run; pnpm and uv have no uncommitted join, so they refuse and name the config line. Watch the real checkouts. One identity for a file reached through the link.                                                                                                                               |
| 2026-11-06, round 9           | Cargo: the Rust spell generates `.magus/gen/cargo-patch.toml` and passes it with `cargo --config` per run. A search that would cross into another workspace is denied by the guard, which names the `magus query` or `magus refs` command. Node and TypeScript realpaths are mapped back to `.magus/workspaces/<name>/...` by the spells in magus's output only; the tools keep their defaults, with no `preserveSymlinks`.                                                                                                                                                                                              |
| 2026-11-06, round 10          | Every spell supports a language join, JavaScript and TypeScript included, generated per run under `.magus/gen/` as go.work is. Reverses round 8's pnpm and uv refusals. A spell refuses only where its tool provably cannot join.                                                                                                                                                                                                                                                                                                                                                                                        |
| 2026-11-06, round 10 verified | pnpm 10.33 and uv 0.12.17 reproduced: a generated pnpmfile passed with `--global-pnpmfile` and `--lockfile-dir` under `.magus/gen/` keeps `pnpm-lock.yaml` byte-identical and needs no pnpm workspaces; a frozen-lockfile run fails, so the spell refuses a join under frozen; `pnpm link` edits committed files and is never used. Python: `PYTHONPATH` with `uv run --frozen` leaves `uv.lock` untouched (`--with-editable` does not shadow). TypeScript: a generated tsconfig extending the committed one, run with `tsc -p`; tsserver does not see it, and the spell says so. npm, yarn and bun were not reproduced. |
| 2026-11-06, round 11          | A magus-only linter that is inert outside magus prints one line per run, `inert here: hostvocab, ruletext, ... (magus-only)`, and the run passes.                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| 2026-11-06, round 12          | npm, yarn and bun refuse a joined build with `workspace-join-unverified` until each one's join is reproduced, bun first. A Python join sets `PYTHONPATH` over the project's own venv; no venv is made under `.magus/gen/`.                                                                                                                                                                                                                                                                                                                                                                                               |
| 2026-11-06, layering          | Every language-specific integration lives in that language's Buzz spell (go.work and `GOWORK`, Cargo `[patch]`, the pnpm, TypeScript and Python joins, golangci composition, gopls and tsc identity), never in the Go core. Core knows only the list, the links, row labels, generic refusals and cache identity.                                                                                                                                                                                                                                                                                                        |
| 2026-11-06                    | `.magus/workspaces/<name>` is a directory symlink, a junction on Windows, never hard links (see [Why a symlink](#why-a-directory-symlink-and-not-hard-links)).                                                                                                                                                                                                                                                                                                                                                                                                                                                           |

Superseded by the reframe, and recorded under Amendments: the committed `use` key, `ref`,
the magus.lock pin, `replace`, `share` and the `@name/` marker.

### Facts about this tree

Each claim was read at 785f07b48.

| Fact                                                                                                                                                                                                                                                                                                                                                                                                                                                      | Evidence                                                                                      |
| --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `knowledge.workspaces` is a list of bare roots, env-only in the reference (`MAGUS_KNOWLEDGE_WORKSPACES`, comma-separated), read only by `--global`.                                                                                                                                                                                                                                                                                                       | internal/config/config.go:757-763; docs/reference/config.md `knowledge` table                 |
| Config merges defaults, then `$XDG_CONFIG_HOME/magus/`, then the workspace root, then a cwd below it.                                                                                                                                                                                                                                                                                                                                                     | internal/config/load.go:55-118                                                                |
| The repository tier already refuses one personal key, `sources`.                                                                                                                                                                                                                                                                                                                                                                                          | load.go:197-200                                                                               |
| docs/reference/config.md:3 names three layers and omits the user tier.                                                                                                                                                                                                                                                                                                                                                                                    | config.md:3 (stale)                                                                           |
| `--global` qualifies every id, the current workspace's too, as `<basename>//<id>`; it rewrites ids only, never `Source`, `Attrs` or `Label`; nothing parses `//` back; the prefixed ids break the `project=` and `target:` parsers, untested.                                                                                                                                                                                                             | knowledge.go:147; graph.go:383-397; `magus refs QualifierSep`: graph.go:384, graph_test.go:39 |
| An unopenable registered root is skipped with a log warning; two roots with one basename merge.                                                                                                                                                                                                                                                                                                                                                           | knowledge.go:165, 194-197                                                                     |
| `--global` builds each registered workspace's graph with the reading binary, into that workspace's own `.magus/`, and bypasses the server.                                                                                                                                                                                                                                                                                                                | knowledge.go:175-190; query.go:143                                                            |
| Ids are `<kind>:<qualified-name>`, minted only in internal/graph/knowledge/id.go; about 48 sites in about 28 files parse them with `CutPrefix(id, kind+":")`. Path-bearing kinds (`file:`, `dir:`, `project:`, `target:<project>:<name>`, `function:<path>:<name>`, `docsection:<path>#<anchor>`) hold workspace-relative paths.                                                                                                                          | id.go; seams report, `nodeProjectPath` query.go:1432, `projectOfTargetID` routing.go:185      |
| `symbol:` ids are SCIP monikers that already name their module.                                                                                                                                                                                                                                                                                                                                                                                           | `magus refs Workspaces` output                                                                |
| The graph is per checkout under `<cacheDir>/knowledge/`; `foldKnowledgeSite` folds the root and cache dir into every stamp; nothing watches a registered workspace.                                                                                                                                                                                                                                                                                       | knowledge.go:855; symbol.go:381                                                               |
| The ranking's one multiplicative site is the per-kind `prior`; matches are built in one place, `newMatch`.                                                                                                                                                                                                                                                                                                                                                | retrieve.go:424, 449                                                                          |
| `NewStore` has 11 production callers; `loadWorkspaceGraph` has 9 occurrences in 4 files and is the join point.                                                                                                                                                                                                                                                                                                                                            | seams report                                                                                  |
| A symlinked source is skipped by the hash walk.                                                                                                                                                                                                                                                                                                                                                                                                           | internal/cache/hash_test.go:147-165, 867                                                      |
| `.magus/` is ignored by this repository's VCS.                                                                                                                                                                                                                                                                                                                                                                                                            | .gitignore:105                                                                                |
| `cache.dir` can move the cache dir out of `<root>/.magus`.                                                                                                                                                                                                                                                                                                                                                                                                | internal/config/cache_dir.go:15-23                                                            |
| A hit stays in its checkout; a replayed log naming paths outside the checkout is the MGS8005 error.                                                                                                                                                                                                                                                                                                                                                       | ADR 0017                                                                                      |
| A checkout's own `./magus` judges calls that land in it; a leased write into another magus checkout is denied.                                                                                                                                                                                                                                                                                                                                            | changes/unreleased/guard-judges-with-checkout-binary.md, guard-lease-checkout.md              |
| A's `.golangci.yml` is not portable: every conventions plugin's settings name `module: github.com/egladman/magus` and refuse another module, and path-scoped allows (diagmsg's `cmd/magus/spell.go`) fail when they match nothing in B.                                                                                                                                                                                                                   | prototype proof, 2026-11-06, refs out28c63b92ea03, outad2c1de38d9b                            |
| On the prototype, editing A's `.golangci.yml` misses B's lint cache, and the same bytes from another root hit.                                                                                                                                                                                                                                                                                                                                            | refs out7118a1732fa0, out4b77ab36d269                                                         |
| `nolintlint` is enabled and requires a reason on every `//nolint`.                                                                                                                                                                                                                                                                                                                                                                                        | .golangci.yml:93, 154                                                                         |
| ripgrep, BSD and GNU `grep -r`, `find`, fd, telescope and git grep do not follow a directory symlink by default; Claude Code's Grep also skips gitignored paths unless given one; `go list ./...` skips symlinks and dot directories.                                                                                                                                                                                                                     | research-join-deep.md, verified by fetch and reproduced on macOS, 2026-11-06                  |
| FSEvents does not report a change in a link's target to the directory holding the link; VS Code does not watch a symlinked folder unless `files.watcherInclude` names it; fsnotify has no recursive watch.                                                                                                                                                                                                                                                | the same report, from Apple's, VS Code's and fsnotify's sources                               |
| Go keeps the logical path through a link; Node and TypeScript resolve the realpath and can load one module twice.                                                                                                                                                                                                                                                                                                                                         | the same report                                                                               |
| `GOWORK=<path>` selects any go.work file; committing one is "generally inadvisable" in Go's reference.                                                                                                                                                                                                                                                                                                                                                    | the same report                                                                               |
| pnpm honors `--global-pnpmfile`; a generated pnpmfile whose `readPackage` rewrites a dependency to `link:<abs>`, with `--lockfile-dir` in a gen dir, resolves to the other checkout and leaves `pnpm-lock.yaml` byte-identical; under `--frozen-lockfile` it fails with `ERR_PNPM_LOCKFILE_CONFIG_MISMATCH` (pnpmfile checksum); `pnpm link` rewrites `pnpm-workspace.yaml` and the lock; `link:` installs none of the linked package's own dependencies. | research-js-python-joins.md, reproduced with pnpm 10.33.0, 2026-11-06                         |
| `PYTHONPATH=<other source root>` with `uv run --frozen` imports the other checkout and leaves `uv.lock` untouched; `uv run --with-editable` keeps the registry copy; uv.toml `sources` is an error.                                                                                                                                                                                                                                                       | the same report, uv 0.12.17                                                                   |
| A tsconfig `extends` resolves from its own file; `include` in the child replaces the base's; `paths` maps types only, not runtime.                                                                                                                                                                                                                                                                                                                        | the same report, from the tsconfig reference                                                  |
| npm, yarn and bun were read from docs only; `bun link` defaults to `--no-save`.                                                                                                                                                                                                                                                                                                                                                                           | the same report                                                                               |

### The prototype

Branch `use-workspaces-core` (cdd31ac96 to 17020e13f) built the superseded shape: a
committed `use` key, `share`, the `lint` kind, `@name/` references (`types.UseRef`,
`CutUseRef`), a link dir `.magus/use/@<name>` (`types.UseLinkDir`) that the module search
takes as a root, MGS1054 `UseWorkspaceUnavailable`, a read-only sandbox entry per used
root, and `magus\describe.workspace()`'s `use` field.
[What the prototype renames](#what-the-prototype-renames) maps each to this record.

## Decision

### 1. One key connects workspaces

```yaml
# $XDG_CONFIG_HOME/magus/magus.yaml (the person's file, in no repository)
knowledge:
  workspaces:
    - name: magus
      path: ~/Repos/magus
    - name: buzzed
      path: ~/Repos/buzzed
```

- **Where it is set.** The user tier only. A `knowledge.workspaces` in a repository's
  magus.yaml (root or cwd tier) refuses the load (`workspaces-committed`), as `sources`
  is refused. No flag and no environment variable sets it, so the list is one file a
  person reads, and `MAGUS_KNOWLEDGE_WORKSPACES` is deleted.
- **An entry.** `name` is `[a-z0-9-]+` and unique in the list; `path` is absolute or
  starts with `~`. Bare strings, the old shape, refuse the load with the entry to write.
- **Who is connected.** A workspace is connected when its own root is the `path` of an
  entry. A connected workspace sees every other entry. A workspace not on the list sees
  none and makes nothing on disk, so the list on one machine never reaches a checkout it
  does not name.
- **Identity is the entry name.** Every label, link and id prefix uses `name`, never a
  directory basename and never a worktree name.
- **Both ways.** One list connects every pair. There is no direction to declare.
- **What is committed.** Nothing. Two repositories connect on one machine and remain
  unaware of each other in version control. CI has no user config, so CI connects nothing
  unless its own job writes one.

### 2. What is on disk

For every connected workspace W and every other entry E, the load makes:

```text
<W root>/.magus/workspaces/<E name>  ->  <E path>     directory symlink; a junction on Windows
```

So with the list above:

```text
~/Repos/buzzed/.magus/workspaces/magus   ->  ~/Repos/magus
~/Repos/magus/.magus/workspaces/buzzed   ->  ~/Repos/buzzed
```

- The links live at `<root>/.magus/workspaces/` even when `cache.dir` moves the cache
  dir, because a link must be a path inside the tree to be reached by a plain path.
- `.magus/` must be ignored by the workspace's VCS. A tracked `.magus/workspaces` refuses
  the load (`workspaces-tracked`).
- Every load reconciles the links in both trees of every pair it belongs to: it makes a
  missing link, retargets one whose entry moved, and removes one whose entry is gone. It
  writes only symlinks directly under `.magus/workspaces/`; a regular file or directory
  at that path refuses (`workspaces-not-a-link`), and nothing is ever deleted through a
  link.
- Each change prints one stderr line at every verbosity. A load that changes nothing
  prints nothing ([No news is good news](../design/principles/no-news-is-good-news.md)).
- `magus describe workspace` lists every entry with its path, link, revision and graph
  freshness.

#### Why a directory symlink and not hard links

- Directories cannot be hard-linked on Linux or APFS, so it would take one link per file.
- A file added to A later would never appear in B.
- Editors, gofmt and git save by writing a new file and renaming it over the old one.
  That breaks a hard link silently, and the two copies drift.
- Hard links cannot cross filesystems.
- Removal would be one unlink per file, against "removal is as simple as adding".

Costs: a symlink on Windows needs Developer Mode or administrator rights, hence the
junction, which links only local volumes; a network path refuses.

#### For agents: discover through magus verbs; the link serves paths

The link exists so a known path opens: a Read, an editor, a Buzz import, a read pattern,
`go work use`. It is not a search path. To find what exists in the other workspace, ask
`magus query`, `magus refs` or `magus explain`, which cross by default and label every
row. A search tool walking the tree skips the link, which is accepted:

| Tool                             | Skips the link because                                                                         | To reach the other side                                                                                              |
| -------------------------------- | ---------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| git, git grep                    | `.magus/` is ignored, and git records a symlink, never its tree                                | not needed: the other repository has its own                                                                         |
| ripgrep, Claude Code's Grep      | symlinks are not followed by default, and gitignored paths are skipped                         | pass `.magus/workspaces/<name>` explicitly, or `-L` (ripgrep prints a loop error on the two-way links and continues) |
| `grep -r`, `find`, fd, telescope | symlinks are not followed by default                                                           | `-R` / `-L` / `--follow`; BSD `find -L` stops silently at the loop                                                   |
| `go list ./...`, gopls           | directory patterns skip symlinks and dot directories                                           | import paths through a generated go.work (below)                                                                     |
| VS Code, FSEvents, fsnotify      | a symlinked folder is not watched, and a change in a link's target is not reported at the link | magus watches each real checkout itself                                                                              |

- The guard denies a search that would cross into another workspace (a recursive search
  that follows a link, or one that names `.magus/workspaces/`) and names the `magus query`
  or `magus refs` command that answers it, as the existing search rules (`symbol-search`,
  `search-translation`) deny a search the graph answers (round 9).
- magus's own walker follows only the links under `.magus/workspaces/`, walks each
  connected root once by its realpath so the two-way links never loop, never descends
  into another root's `.magus/`, and reports a dangling link as `workspace-missing`.
- magus watches the real checkout of every connected workspace, never the link.

#### One identity for a file reached through the link: the logical path

A file of magus seen from buzzed has one identity, its logical path through the link,
`.magus/workspaces/magus/<rel>`. Cache keys, graph ids, rows, findings and waivers use
it. The realpath (`~/Repos/magus/<rel>`) is used only to walk each root once and to watch
it. A tool that reports a realpath (Node, TypeScript, anything that canonicalizes) keeps
its own defaults (no `preserveSymlinks`); the language's spell maps the connected root's
prefix back to `.magus/workspaces/<name>/` in magus's output only, before magus keys,
labels or checks it, so the realpath never reaches a key, an id or ADR 0017's replay check
(round 9).

#### Language-native joins

Where a build joins the trees, such as buzzed's Go importing a magus package, the
language's own join is generated from the same list, never written by the person. Each is
the work of that language's spell, which asks core for the connected roots
([Where each part lives](#where-each-part-lives)):

| Ecosystem                          | What its spell does                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                       |
| ---------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Go (spells/golang)                 | writes `.magus/gen/go.work` (`use ./..` and `use ../workspaces/<name>` for each connected workspace whose module the build imports) and sets `GOWORK` to it for every run of a Go op; `go.work.sum` sits beside it. Each run prints `go: GOWORK=.magus/gen/go.work joins magus`. A `go.work` in the tree is left alone and shadowed by `GOWORK`.                                                                                                                                                                                                                                                                                                                                                          |
| pnpm (spells/typescript)           | writes `.magus/gen/pnpm/pnpmfile.cjs`, whose `readPackage` rewrites each dependency a connected workspace provides to `link:<that workspace's package dir>`, and runs `pnpm install --global-pnpmfile=.magus/gen/pnpm/pnpmfile.cjs --lockfile-dir=.magus/gen/pnpm`, printing that line. The joined lock and `node_modules` land in `.magus/gen/pnpm/`; `pnpm-lock.yaml` and `pnpm-workspace.yaml` are never edited, and no pnpm workspace is needed. A joined run under `--frozen-lockfile` refuses (`workspace-join-frozen`), since pnpm's pnpmfile checksum fails it. `link:` installs none of the linked package's own dependencies, so the other checkout needs its own install, which the line says. |
| TypeScript (spells/typescript)     | writes `.magus/gen/tsconfig.json`, which extends the committed `tsconfig.json`, restates its `include` with `../../` prefixes, and adds `paths` to the other workspace's sources through the link; runs `tsc -p .magus/gen/tsconfig.json`. Each run prints `tsc: .magus/gen/tsconfig.json joins magus; the editor's tsserver reads tsconfig.json and does not see this join`. `paths` maps types only; runtime resolution comes from the pnpm join.                                                                                                                                                                                                                                                       |
| Python (spells/python)             | sets `PYTHONPATH` to each connected workspace's source roots, through the link, over the project's own venv and runs `uv run --frozen`, printing the `PYTHONPATH` it set. No venv is made under `.magus/gen/` (round 12). `uv.lock` and `pyproject.toml` are never edited; `--with-editable` is not used, since it does not shadow a locked dependency.                                                                                                                                                                                                                                                                                                                                                   |
| npm, yarn, bun (spells/typescript) | not reproduced. Until a join is verified for one, a joined build through it refuses (`workspace-join-unverified`), naming the manager and the `knowledge.workspaces` line. `bun link` (`--no-save` by default) is verified first (round 12).                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| Cargo (spells/rust)                | writes `.magus/gen/cargo-patch.toml`, a `[patch]` table pointing at `../workspaces/<name>/...` for each connected workspace whose crate the build depends on, and passes it with `cargo --config .magus/gen/cargo-patch.toml` on every run of a Cargo op, printing that line. A rewrite of the committed `Cargo.lock` by that run is reported (round 9).                                                                                                                                                                                                                                                                                                                                                  |

Each generated file starts with a header naming the list's hash; it is rewritten when the
hash changes and deleted when its last entry goes, by the same load that reconciles the
links. CI has no list, so CI builds against the committed pins: a local build can pass
through a join CI lacks, and the printed `GOWORK` line is how a person sees it.

### 3. Adding and removing a workspace

Both are one edit to the user config. Neither needs a subcommand.

#### Adding

1. The edit: add `{name: buzzed, path: ~/Repos/buzzed}` beside an existing
   `{name: magus, path: ~/Repos/magus}`.
2. The next load in either tree makes both links:

   ```text
   ~/Repos/magus$ magus ls
   workspace buzzed: linked .magus/workspaces/buzzed -> ~/Repos/buzzed
   workspace buzzed: linked ~/Repos/buzzed/.magus/workspaces/magus -> ~/Repos/magus
   ...
   ```

3. Left behind: two symlinks, both under ignored `.magus/` directories. A second load
   prints nothing.

#### Removing

1. The edit: delete the `buzzed` entry.
2. The next load in either tree removes both links. A load in buzzed finds its own root
   unlisted and removes its links and their reverse links; a load in magus finds
   `buzzed` gone and does the same from its side:

   ```text
   ~/Repos/magus$ magus ls
   workspace buzzed: removed .magus/workspaces/buzzed
   workspace buzzed: removed ~/Repos/buzzed/.magus/workspaces/magus
   workspace buzzed: dropped its rows from this workspace's graph view
   ...
   ```

3. Left behind: nothing. The links are gone from both trees, and every generated join
   under `.magus/gen/` (go.work, cargo-patch.toml, pnpm/, tsconfig.json) that joined only
   the removed workspace is deleted, while one that still joins another is rewritten
   without it, each with its own line. A joined `node_modules` under `.magus/gen/pnpm/`
   goes with its directory; the committed `node_modules` was never joined. What was kept only for
   the other workspace is gone too: the cross-workspace edges this workspace recorded toward
   it (decision 5) and its recorded freshness. Neither tree's own graph store held the
   other's nodes, so nothing else changes. A second load prints nothing.
4. Cache entries of targets that read through the link stay in the content store, keyed
   by content like every entry, and age out under the normal prune. They answer again
   only if the same bytes are read again.

#### Deleting the other checkout while its entry stays

```text
~/Repos/magus$ rm -rf ~/Repos/buzzed
~/Repos/magus$ magus ls
error [workspace-missing]: knowledge.workspaces entry buzzed points at ~/Repos/buzzed, which does not exist; delete the entry at ~/.config/magus/magus.yaml:6, or restore the checkout
```

The load refuses in every workspace on the list until the entry is deleted or the path
returns. Deleting the entry is the removal above; the dangling link
`.magus/workspaces/buzzed` is removed by that next load.

### 4. Paths: one spelling, the plain path through the link

A path into another workspace is the path through its link, everywhere it appears:

| Where                  | Spelled in buzzed                                                                                                                                                                                      |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| shell, editor, grep    | `.magus/workspaces/magus/.golangci.yml`                                                                                                                                                                |
| read pattern           | `ctx.readsFiles([".magus/workspaces/magus/.golangci.yml"])`                                                                                                                                            |
| Buzz import            | `import ".magus/workspaces/magus/hack/magusfile/run-golangci";`                                                                                                                                        |
| CLI path argument      | `magus describe file .magus/workspaces/magus/internal/config/config.go`                                                                                                                                |
| node id                | `file:.magus/workspaces/magus/internal/config/config.go`, `project:.magus/workspaces/magus/docs`, `target:.magus/workspaces/magus/docs:build`, `docsection:.magus/workspaces/magus/docs/design.md#...` |
| cache key line         | `.magus/workspaces/magus/.golangci.yml <digest>`                                                                                                                                                       |
| a pasted absolute path | `~/Repos/magus/internal/x.go` resolves to `.magus/workspaces/magus/internal/x.go`                                                                                                                      |

- Ids keep `<kind>:<qualified-name>` and every kind keeps its prefix, so the about 48
  `CutPrefix` sites keep working: a path-bearing id from A carries the link path, which
  is a real path in B.
- Kinds that name no path are global and never prefixed: `symbol:` and `package:`
  (monikers, the join between B's call and A's definition), `owner:` and `link:`.
  Registry kinds (`spell:`, `op:`, `charm:`, `module:`, `method:`, `diagnostic:`) are the
  reading binary's, one copy per view.
- The addressing question of earlier drafts (`@magus/path`, `@magus//path`,
  `magus:path`) is answered by the reframe: no marker exists, the link is the address.

### 5. The knowledge graph, and what every verb returns

**Each workspace's graph is built by its own binary in its own root, and read in place.**
A read verb in buzzed opens magus's store through the link,
`.magus/workspaces/magus/<magus cache dir>/knowledge/`, read-only, and presents both
stores as one graph. buzzed never writes magus's store, and never evaluates magus's
magusfile to answer a read ([ADR 0014](0014-running-magus-is-the-consent.md)). This is
the stack-of-indexes model of prior art (Glean's stacked databases, stack graphs'
query-time merge), held to magus's rule that a verdict about a tree comes from that
tree's own binary ([sibling-checkout](../reference/rules/sibling-checkout.md#why)).

The view is assembled at `loadWorkspaceGraph`:

- **Layers.** One per connected workspace, in a fixed order: the workspace the command
  runs in, then every other entry in list order. Each layer is that workspace's store,
  unchanged.
- **Ids.** A layer's path-bearing ids are rewritten through its link as it is read
  (decision 4); global kinds are not.
- **Cross edges.** Recorded in the reading workspace's own store, in one shard per
  connected workspace, from declarations only: a read pattern through a link (`reads`),
  a Buzz import through a link (`imports`), and a SCIP occurrence in one workspace of a
  moniker another defines (`references`). A `package:` node gets a `provided_by` edge to
  the workspace whose `go.mod` (or other manifest) declares that module. Nothing is
  inferred across.
- **Symbols.** A store set over each layer's symbol store replaces the single `Store` for
  the callers that answer symbols (`refs`, symbol-seeded `query`, `explain`).
  A moniker defined in two layers lists both definitions and says so. A reference to a
  moniker no layer defines is `dangling`, so a rename in A is a visible break in B.

Per row, JSON carries:

| Field        | Value                                                                                          |
| ------------ | ---------------------------------------------------------------------------------------------- |
| `workspace`  | the entry name, for every row, the reading workspace's included                                |
| `revision`   | the commit the layer's store was built at, with `+dirty` when the tree had uncommitted changes |
| `indexed_at` | when that store was built                                                                      |
| `freshness`  | `fresh`, `stale` (the tree changed since), or `unknown` with a cause                           |

Text shows the workspace through the path itself; a row whose id holds no path is
suffixed `(magus)`. A row that is not fresh is marked `stale` or `unknown`. A header line
names each layer that is not fresh and the command that fixes it; when every layer is
fresh, it is omitted.

Freshness of another layer is judged from its own recorded stamp against its tree
(VCS revision and a dirty-tree digest), never by binary identity, since the binaries
differ by construction.

| Verb                                          | Crosses                                           | Returns                                                                                                                                                                                      |
| --------------------------------------------- | ------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `query`                                       | yes                                               | ranked rows from every layer, labeled; `workspace=<name>` keeps one layer, and a term holding `.magus/workspaces/magus` scopes as any path term does                                         |
| `explain`, `path`                             | yes                                               | a node from any layer, edges across layers                                                                                                                                                   |
| `refs`                                        | yes                                               | definitions and occurrences in every layer, each path through its link                                                                                                                       |
| `describe file`                               | yes                                               | for a path through a link, the owning workspace's own classification of it                                                                                                                   |
| `describe rules <path>`                       | yes                                               | every rule that applies to the path, from either workspace (decision 7)                                                                                                                      |
| `describe workspace`                          | n/a                                               | every entry: name, path, link, revision, freshness                                                                                                                                           |
| `ls`, `describe projects`, `describe targets` | yes                                               | every layer's projects and targets, labeled; another workspace's project path is its path through the link                                                                                   |
| `graph stats`, `graph export`, `graph diff`   | yes                                               | the stacked view, every node with `workspace`                                                                                                                                                |
| `graph build`                                 | yes                                               | builds each connected workspace's store, each by its own `./magus` in its own root, one line per workspace; a workspace with no `./magus` refuses (`workspace-binary-absent`) for that layer |
| `run`, `x`                                    | only when a path through a link names the project | that workspace's own binary runs it in its own root, one line saying so; with no project argument, only this workspace's projects                                                            |
| `affected`                                    | yes, for seeds and listing                        | decision 6                                                                                                                                                                                   |
| `job`                                         | no                                                | a job's write paths are its own workspace's; decision 8                                                                                                                                      |

Read verbs never build. A stale or missing layer answers `stale` or `unknown`, never
`absent`: `unknown` causes are `workspace-graph-absent` (no store built), `workspace-graph-schema`
(a `schema_version` this binary does not read) and `workspace-missing` (decision 3).

#### Weighting toward here

`knowledge.workspace_weight` (config key, and the generated flag
`--knowledge-workspace-weight`) multiplies the lexical score of every row from another
workspace, beside the kind prior in `prior()`. It is named in `weights.json` and in each
match's `why`. Default `0.7`: a row from here outranks an equally good row from there, and
a strong row from there still appears. The graph list is unweighted: reader seeds from
this workspace flow across cross edges, so a symbol B calls pulls A's definition up.
`1.0` ranks every workspace alike; `workspace=.` keeps only here. The default is
provisional until an eval reports precision and recall for local-first questions
("what already does this", "who calls X"), which is a phase-3 gate (round 6).

#### What goes

`--global`, `BuildGlobal`, `Qualified`, `QualifierSep`, `workspaceName` (the basename),
the skip at knowledge.go:165, the server bypass at query.go:143 and the build of another
workspace's graph by the reading binary (knowledge.go:175-190). magus is pre-1.0; the
break is recorded and ships no alias
([A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md)).

#### Phases, each shippable alone

| Phase | Ships                                                                                                                                                                                                                                                                 | Seams touched                                                         |
| ----- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| 1     | named entries, user tier only, the refusals, links made and removed on load, `describe workspace`                                                                                                                                                                     | config.go, load.go, validate.go; prototype's link code renamed        |
| 2     | reads and imports through links hashed under link-relative names; the walk follows exactly the links a pattern names; a target reaching an absent link refuses                                                                                                        | internal/cache/hash.go walk, the module search root                   |
| 3     | stacked graph view at `loadWorkspaceGraph`, ids rewritten through links, `workspace` and freshness on rows (`newMatch`), the weight in `prior()`, `--global` deleted; domain shards only                                                                              | knowledge.go, query.go, retrieve.go:424 and 449, foldKnowledgeSite    |
| 4     | the symbol store set over the `NewStore` callers that answer symbols; moniker joins, `dangling`, `provided_by`                                                                                                                                                        | query.go:777, refs.go:400, knowledge.go:1273, store.go                |
| 5     | affected seeds and listing across workspaces; `graph build` per workspace                                                                                                                                                                                             | affected, graph.go                                                    |
| 6     | the server's warm view, and watchers on every connected workspace's real checkout                                                                                                                                                                                     | server.go:913, symbol.go:381                                          |
| 7     | the generated joins: go.work with `GOWORK`, cargo-patch.toml with `cargo --config`, the pnpmfile with `--global-pnpmfile` and `--lockfile-dir`, the tsconfig with `tsc -p`, `PYTHONPATH` with `uv run --frozen`; `workspace-join-frozen`, `workspace-join-unverified` | the Go, Rust, TypeScript and Python spells; the load's reconcile step |

### 6. affected, the cache and CI

- **Cache key.** A file read through a link is keyed by its logical path
  (`.magus/workspaces/magus/<rel>`, decision 2) and its content. The key names neither root, so the
  same bytes hit from any checkout, as the prototype showed (out4b77ab36d269).
- **The walk.** `.magus/` stays pruned, so `**/*.go` never descends into another
  workspace. A pattern that names `.magus/workspaces/<name>/...` explicitly is walked
  through that one link, which changes the symlink skip (hash_test.go:147-165) for these
  links only.
- **Replayed logs.** A path under a link is inside the checkout. A tool that prints the
  resolved absolute path has it rewritten to the link path before ADR 0017's check, so
  MGS8005 does not fire for a connected workspace.
- **affected seeds from every connected workspace's diff.** Each workspace contributes
  its own diff against its own affected base, spelled through its link. A target is
  affected when a read pattern or import matches one. Each seed row says why:
  `magus changed: .magus/workspaces/magus/libs/conventions/errmsg/errmsg.go`.
- **affected lists across.** Targets in every workspace are listed, labeled. Executing
  them stays in each workspace: `magus affected ci` in buzzed lists magus's affected
  targets and runs buzzed's only (round 6).
- **CI.** No user config, so no links. A target whose read pattern or import reaches
  `.magus/workspaces/<name>` refuses (`workspace-not-connected`); every other target
  runs. A CI that wants the other workspace checks it out and writes the user config
  itself; magus adds nothing for it.
- Each connected root is a read-only sandbox entry for this workspace's targets; output
  built from it lands in this workspace's `.magus/`.

### 7. Rules across workspaces, from the person's side

Three rules hold for every kind:

1. **A rule applies to a file only where that file's workspace adopts it.** Connecting a
   workspace adopts nothing. buzzed's files obey magus's rules only where buzzed's own
   magusfile names them through the link.
2. **An exception is written in the adopting workspace, at the line or where the rule is
   adopted.** buzzed never edits magus to change a rule for buzzed, and never keeps a
   copy.
3. **Every finding names its rule's workspace, the rule's definition file and line, and
   the fix command with the workspace it runs in.** A rule from another workspace is
   labeled `[errmsg from magus]`; buzzed's own is `[errmsg]`.

| Kind                                               | Defined                                                 | Applies to                                                                                   | When both define one                                                       | B turns one off, tightens, adds                                                                  | Finding prints                                                                                                                    |
| -------------------------------------------------- | ------------------------------------------------------- | -------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------- |
| lint config and custom analyzers                   | A's `.golangci.yml`, analyzer sources                   | B's Go, where B's lint target reads A's config through the link                              | B adopts A's config unchanged, or keeps its own; never a merge             | `//nolint:<rule> // <reason>` at the line; to add, B's own target runs B's own config beside A's | `[errmsg from magus]`, analyzer file:line, A's config line, fix here or `magus run lint .magus/workspaces/magus/libs/conventions` |
| proofread                                          | A's rule decisions                                      | B's prose, where B's proofread target adopts them                                            | per rule id, B's decision wins                                             | B's decision for that id                                                                         | `[PRF4001 filler from magus]`, A's decision line                                                                                  |
| hack-lint scripts (Buzz returning `types.Finding`) | A's `hack/lint/*.buzz`                                  | B's files, for each script B imports through the link                                        | add: B's own scripts run beside them                                       | drop the import; write B's own                                                                   | `[<rule> from magus]`, the script line                                                                                            |
| conformance checks                                 | each workspace's own `knowledge.conformance`            | that workspace's graph only                                                                  | none: committed policy is never read across (`cli:"-"`, config.go:810-815) | B writes its own                                                                                 | `[<check>]` in B's own terms                                                                                                      |
| precedent families                                 | computed from each workspace's own graph and gates      | read verbs, which cross                                                                      | add, each labeled and judged by its own gates                              | `workspace=.` scopes one query                                                                   | the family's workspace and members                                                                                                |
| guard policy                                       | each workspace's `magus\guard.builtins` and policy Buzz | its own files always (decision 8); B's files too where B's root magusfile imports A's policy | per rule name, B's setting wins on B's files; A's other rules still apply  | B's own `guard.builtins` entry for that name                                                     | `deny [lease-write from magus]: ...`, the policy line                                                                             |
| design principles                                  | each workspace's `docs/design/`                         | read, never enforced                                                                         | add, labeled                                                               | nothing to turn off                                                                              | page and anchor, labeled                                                                                                          |

#### Lint: magus's rules are portable, and buzzed takes them unchanged

The prototype showed A's `.golangci.yml` could not be read by B as it stood: every
conventions plugin was configured with `module: github.com/egladman/magus` and refused
B's module, and A's path-scoped allows matched nothing in B and failed. Round 6 answered
with a composing call; round 7 reverses that and fixes the rules at the source:

- Each conventions plugin reads the module of the tree it lints, from that tree's
  `go.mod`, instead of a configured module path.
- magus-only path rules (diagmsg's `cmd/magus/spell.go`, every allow naming magus's
  paths) move into a part of `.golangci.yml` that matches only in magus, so in another
  tree they neither apply nor fail.
- buzzed's lint target reads `.magus/workspaces/magus/.golangci.yml` and the plugin
  sources through the link and runs them unchanged. A rule magus adds reaches buzzed on
  its next run with no buzzed change.
- An exception is `//nolint:<rule> // <reason>` at the line, in buzzed's file.
  nolintlint, which magus's config enables (.golangci.yml:93), requires the reason and
  reports a directive that suppresses nothing, which fails the run (round 5).
- There is no lint argument, overlay or composed config. The job
  `portable-conventions-lint` builds the portable rules on its own branch.
- A finding from that job (5fe2bd3e7): magus-only settings move under
  `settings.in-module.github.com/egladman/magus` in `.golangci.yml`, which applies only in
  that module and modules nested in it. A linter whose settings are only such blocks
  (`hostvocab`, `ruletext` and others whose every setting names magus's paths) builds no
  analyzer in buzzed, and nothing says so. A rule buzzed reads as enforced does nothing,
  which [No silent defaults](../design/judgment/no-silent-defaults.md) forbids. So the Go
  spell prints one line per run naming them, and the run passes (round 11):
  `lint .: inert here: hostvocab, ruletext, ... (magus-only)`.

```buzz
// buzzed/magusfile.buzz: the whole adoption
import ".magus/workspaces/magus/hack/magusfile/run-golangci" as golangci;
golangci.run({config: ".magus/workspaces/magus/.golangci.yml"});
```

#### Waivers and accepted findings

- A waiver lives with the file the finding is about. A rule of A firing in B's file is
  waived in B: inline at the line, or in B's project ledger
  `<project>/accepted.jsonl` ([ADR 0021](0021-a-change-accepts-the-findings-it-causes.md)).
  B never writes A's ledgers, and A's never excuse B's findings.
- A ledger line names the rule with its workspace (`"rule": "errmsg from magus"`). A
  waiver of `errmsg` alone matches only B's own rule, so a waiver never silently covers
  another workspace's rule. `types.JobWaiver` matching is otherwise unchanged.

#### When A changes a rule

A's working tree is read live, committed or not. The next run in B that adopts the rule
misses its cache on A's changed file and says so, then prints the findings diff against
its previous run:

```text
lint .: rerun: magus changed .magus/workspaces/magus/libs/conventions/errmsg/errmsg.go
lint .: 1 finding gone: editor/save.go:42 [errmsg from magus]
```

#### The guard with both policies loaded

- A call is judged per path by the path's own workspace, with that workspace's policy,
  evaluated by that workspace's own `./magus`. A call naming no path is judged by the
  workspace its line places it in ([ADR 0015](0015-a-call-runs-where-its-line-says.md)).
- A call touching both trees is judged by both, and any deny denies. The verdict prints
  one line per path:

  ```text
  deny [lease-write from magus]: .magus/workspaces/magus/libs/conventions/errmsg/errmsg.go is in magus, where this session holds no lease; fork one there: magus run ... in ~/Repos/magus
  ok   [lease-write]: editor/save.go is inside lease editor-errors
  ```

- When the two policies disagree on one path, the path's workspace decides. B's setting
  of a rule name wins only on B's files.

#### Which rules apply here, and from where

`magus describe rules <path>` (today `describe rules` lists the guard's rules; it gains a
path argument) lists every rule of every kind that applies to that path, with its
workspace, its definition's file and line, its decision, and every exception that names
it in that file:

```text
~/Repos/buzzed$ magus describe rules editor/save.go
lint      [errmsg from magus]   deny  defined .magus/workspaces/magus/libs/conventions/errmsg/errmsg.go:31; config .magus/workspaces/magus/.golangci.yml:118; excepted editor/save.go:42 ("editor errors predate errmsg")
lint      [stutter from magus]  deny  defined .magus/workspaces/magus/libs/conventions/stutter/stutter.go:22
guard     [lease-write]         deny  built in; this file is judged by buzzed
```

#### Walkthrough: buzzed hits errmsg, makes an exception, fixes it in magus

Output lines are proposed, not built. The user config lists `magus` and `buzzed`. An
exception is per line; round 7 leaves no package-wide narrowing.

1. buzzed's lint finds the rule:

   ```text
   ~/Repos/buzzed$ magus run lint .
   lint .: 1 finding
     editor/save.go:42:9: [errmsg from magus] an error joins two clauses with "and"
       rule    .magus/workspaces/magus/libs/conventions/errmsg/errmsg.go:31; config .magus/workspaces/magus/.golangci.yml:118
       fix     here: split the error in editor/save.go
       rule    in magus: magus run lint .magus/workspaces/magus/libs/conventions
       except  editor/save.go:42: //nolint:errmsg // <reason>
   ```

2. The person adds `//nolint:errmsg // the analyzer misreads "and" in a quoted name` at
   line 42:

   ```text
   ~/Repos/buzzed$ magus run lint .
   lint .: passed
   ```

3. The person fixes the analyzer in magus from the same session; magus's guard judges the
   write, and magus's own binary runs magus's test:

   ```text
   ~/Repos/buzzed$ magus run test .magus/workspaces/magus/libs/conventions -- -run 'TestErrmsg'
   test .magus/workspaces/magus/libs/conventions: run by magus in ~/Repos/magus
   test .magus/workspaces/magus/libs/conventions: passed
   ~/Repos/buzzed$ git -C ~/Repos/magus commit -m "skip a conjunction inside a quoted name in errmsg"
   ```

4. buzzed sees it on the next run, and the exception now suppresses nothing:

   ```text
   ~/Repos/buzzed$ magus run lint .
   lint .: rerun: magus changed .magus/workspaces/magus/libs/conventions/errmsg/errmsg.go
   lint .: 1 finding
     editor/save.go:42:1: [nolintlint] directive //nolint:errmsg is unused for linter "errmsg"
   ```

   The person deletes the directive, and the next run passes.

5. buzzed's CI has no user config, so its lint target, which imports through
   `.magus/workspaces/magus`, refuses there with `workspace-not-connected` until the CI
   job checks magus out and writes the user config itself.

### 8. Editing across workspaces

- The session crosses freely: it may read and write either tree.
- A write is judged by the file's own workspace: its guard policy, evaluated by its own
  `./magus`; its leases; its lint. A write into a workspace with no `./magus` refuses
  (`workspace-binary-absent`), naming that workspace's bootstrap.
- A job's write paths are its own workspace's. A fork whose write path resolves through a
  link refuses (`workspace-write-path`), naming the other workspace's checkout to fork in.
- A change is committed in its own repository; a commit never holds the other's files,
  and the links are never committed (decision 2).
- Each connected root is read-only to the other workspace's targets.

### 9. Version skew

- A workspace's `required_version` applies whenever another workspace's binary evaluates
  its Buzz (an import through a link) or reads its store. Below it, the import or read
  refuses (`workspace-version-floor`), naming both versions.
- An import through a link that calls a binding the reading binary lacks fails the Buzz
  check at load, with the link path in the error. No compat shim is added.
- A store whose `schema_version` the reading binary does not read answers `unknown`.
- Writes are judged by the owning workspace's own binary, so guard skew does not arise.

### 10. Refusals

Codes in the MGS10xx magusfile family are assigned when built; the prototype took MGS1054.

| Name                                               | When                                                                                         | It says                                                                                                                                                                            |
| -------------------------------------------------- | -------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `workspaces-committed`                             | `knowledge.workspaces` is in a repository's magus.yaml                                       | `knowledge.workspaces is personal: move it from <file>:<line> to $XDG_CONFIG_HOME/magus/magus.yaml`                                                                                |
| `workspaces-entry-shape`                           | a bare string, a bad name, a duplicate name, a relative path                                 | the entry and the shape to write                                                                                                                                                   |
| `workspace-missing`                                | an entry's path does not exist or holds no magus workspace                                   | `entry <name> points at <path>, which does not exist; delete the entry at <file>:<line>, or restore the checkout`                                                                  |
| `workspaces-tracked`                               | `.magus/workspaces` is tracked by the VCS                                                    | `.magus/ must be ignored: add it to .gitignore`                                                                                                                                    |
| `workspaces-not-a-link`                            | a non-symlink sits at `.magus/workspaces/<name>`                                             | the path, and that magus does not delete it                                                                                                                                        |
| `workspace-not-connected`                          | a target's read pattern or import reaches `.magus/workspaces/<name>` and no such link exists | `<project>:<target> reads .magus/workspaces/<name>, which is not connected here; add {name: <name>, path: ...} to knowledge.workspaces in $XDG_CONFIG_HOME/magus/magus.yaml`       |
| `workspace-binary-absent`                          | a build, run or write lands in a workspace with no `./magus`                                 | the workspace and its bootstrap command                                                                                                                                            |
| `workspace-write-path`                             | a job fork names a write path through a link                                                 | the workspace to fork in                                                                                                                                                           |
| `workspace-version-floor`                          | the reading binary is below the other's `required_version`                                   | both versions                                                                                                                                                                      |
| `workspace-join-frozen`                            | a pnpm install that would join a connected workspace runs under `--frozen-lockfile`          | `pnpm cannot join magus under --frozen-lockfile (the pnpmfile checksum fails); run without it, or remove the entry at <file>:<line>`                                               |
| `workspace-join-unverified`                        | a joined build through npm, yarn or bun                                                      | `<manager> has no verified join that leaves committed files alone; this build uses the committed dependency; the connection is knowledge.workspaces entry <name> at <file>:<line>` |
| `workspace-graph-absent`, `workspace-graph-schema` | another layer has no store, or one this binary cannot read                                   | an `unknown` verdict with `magus graph build`                                                                                                                                      |

### Where each part lives

Eli's rule: every language-specific integration lives in that language's Buzz spell,
never in the Go core. Core knows only what holds for every language: the workspace list,
the links, row labels, the generic refusals and cache identity. A spell asks core for each
connected workspace's name and root, through `magus\describe.workspace().workspaces`, and
does its language's part through the [spell contract](../reference/spell-contract.md),
as every language engine already does ([Engines](../concepts/engines.md#the-spell-contract)).

| Part                                                                                                                                                                                 | Lives in                                                       | Path                                                                           |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------- | ------------------------------------------------------------------------------ |
| `knowledge.workspaces` entries, user tier only, the shape refusals                                                                                                                   | core                                                           | internal/config/config.go, load.go, validate.go                                |
| links made, retargeted and removed on load, in both trees                                                                                                                            | core                                                           | the load's reconcile step, beside `ward.CheckRequiredVersion` (internal/ward/) |
| the list and roots exposed to Buzz                                                                                                                                                   | core                                                           | `magus\describe.workspace()` (internal/interp/bindings/describe.go)            |
| cache identity: the logical path through a link; the walk following only those links                                                                                                 | core                                                           | internal/cache/hash.go                                                         |
| the read-only sandbox entry per connected root                                                                                                                                       | core                                                           | internal/sandbox/defaults.go                                                   |
| Buzz imports through a link                                                                                                                                                          | core (Buzz is magus's own language)                            | internal/interp/runtime.go module search                                       |
| the stacked graph view, ids through links, row labels, freshness, the weight                                                                                                         | core                                                           | knowledge.go, internal/graph/knowledge/, retrieve.go                           |
| guard: both policies loaded, each path judged by its owner; the steer from a crossing search                                                                                         | core                                                           | internal/guard/                                                                |
| generic refusals (`workspace-missing`, `workspace-not-connected`, `workspaces-*`, `workspace-binary-absent`, `workspace-write-path`, `workspace-version-floor`, `workspace-graph-*`) | core                                                           | types/diagnostic.go, docs/reference/codes/magusfile/                           |
| a refusal API a spell raises with its own text                                                                                                                                       | core                                                           | the spell contract's failure result                                            |
| generated `.magus/gen/go.work`, `GOWORK` per Go op, the printed `GOWORK` line                                                                                                        | the Go spell                                                   | spells/golang/spell.buzz, spells/golang/gomod.buzz                             |
| golangci-lint over another workspace's config and custom plugin build                                                                                                                | the Go spell                                                   | spells/golang/spell.buzz (`golangCILint`)                                      |
| portable conventions plugins (read the linted tree's go.mod)                                                                                                                         | the plugins themselves                                         | libs/conventions, `.golangci.yml` (job portable-conventions-lint)              |
| gopls identity: the logical path through the link                                                                                                                                    | the Go spell                                                   | spells/golang/spell.buzz                                                       |
| the pnpm join: generated `.magus/gen/pnpm/pnpmfile.cjs`, `--global-pnpmfile` and `--lockfile-dir`, the `workspace-join-frozen` refusal                                               | the TypeScript spell (it drives JavaScript's package managers) | spells/typescript/spell.buzz (`pnpmInstall`)                                   |
| the npm, yarn and bun refusal (`workspace-join-unverified`) until a join is verified                                                                                                 | the TypeScript spell                                           | spells/typescript/spell.buzz (`npmInstall`)                                    |
| the TypeScript join: generated `.magus/gen/tsconfig.json`, `tsc -p`, the tsserver line; mapping tsc's and Node's realpaths back to the link path in magus's output                   | the TypeScript spell                                           | spells/typescript/spell.buzz                                                   |
| the Python join: `PYTHONPATH` through the link with `uv run --frozen`                                                                                                                | the Python spell                                               | spells/python/spell.buzz (`uvSync` and the run ops)                            |
| generated `.magus/gen/cargo-patch.toml`, `cargo --config` per Cargo op                                                                                                               | the Rust spell                                                 | spells/rust/spell.buzz                                                         |
| this repository's own lint wiring                                                                                                                                                    | this repository's magusfile, composing the Go spell only       | magusfile.buzz                                                                 |

`workspace-join-frozen` and `workspace-join-unverified` are raised by the TypeScript spell
through core's refusal API; core holds the codes and their pages, never the ecosystem's
reason.

Pieces the prototype put in the wrong layer:

- `hack/magusfile/run-golangci.buzz` (fbe021cb9) composes the golangci config and builds
  custom-gcl in repository Buzz. That is a Go mechanism, so it belongs in spells/golang;
  the proof's import of it through the link goes away with it.
- `types.ShareKind` and `ShareLint` (types/use.go) name a lint tool's kind in Go core.
  They are deleted (decision 1 has no kinds).
- Nothing else in the prototype is language-specific: the hash walk, the sandbox entry,
  the module search root and the load refusal are core's.

### What the prototype renames

| Prototype (`use-workspaces-core`)                          | Under this record                                              |
| ---------------------------------------------------------- | -------------------------------------------------------------- |
| `use:` in magus.yaml, `config.UseEntry{Name, Path, Share}` | `knowledge.workspaces` entries `{name, path}` in the user tier |
| `.magus/use/@<name>`, `types.UseLinkDir`                   | `.magus/workspaces/<name>`                                     |
| `types.UseRef`, `CutUseRef`, `useRefPrefix`                | deleted; paths through links                                   |
| `types.ShareKind`, `ShareLint`                             | deleted                                                        |
| MGS1054 `UseWorkspaceUnavailable`                          | `workspace-missing`                                            |
| `@name/` read patterns in internal/cache/hash.go           | link-relative paths; the walk follows named links              |
| the refusal of escaping read patterns                      | kept                                                           |
| `magus\describe.workspace().use`, `types.UsedWorkspace`    | `.workspaces`, one record per entry                            |
| magus_use.go, internal/ward/use.go                         | renamed for workspaces; the load reconciles links              |
| changes/unreleased/use-stitches-workspaces.md              | rewritten for this record                                      |
| `import "@magus/hack/magusfile/run-golangci"` in the proof | `import ".magus/workspaces/magus/hack/magusfile/run-golangci"` |

### State

| Item                                                                                                                           | State                                                                                |
| ------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------ |
| Named entries in the user tier; repository tier refused                                                                        | done (MGS1055, MGS1056)                                                              |
| Links made, retargeted and removed by every load, in both trees                                                                | done (internal/ward/workspaces.go; MGS1054, MGS1057, MGS1058)                        |
| Reads through a link keyed by link-relative name; cache independent of the root                                                | done (internal/cache/hash.go)                                                        |
| Imports through a link                                                                                                         | done (a plain path import; `spells.IsRemoteImport` takes a dot-led segment as local) |
| A target reaching a connection that is absent refuses                                                                          | done (MGS1059, for a read pattern and for an import)                                 |
| Read-only sandbox entry per connected root                                                                                     | done                                                                                 |
| `describe workspace` lists every entry                                                                                         | done (name, path, link; revision and freshness wait for phase 3)                     |
| golangci composition in the Go spell                                                                                           | done (spells/golang/golangci.buzz)                                                   |
| `--global` over named entries; the skip and the basename merge refused                                                         | done (`--global` itself goes in phase 3)                                             |
| Stacked graph view, labels, freshness, weight; `--global` deleted                                                              | proposed                                                                             |
| Symbol store set and moniker joins                                                                                             | proposed                                                                             |
| affected across workspaces                                                                                                     | proposed                                                                             |
| Writes judged by the file's workspace; guard loads both policies                                                               | proposed                                                                             |
| Portable conventions rules; buzzed takes magus's config unchanged, exceptions by `//nolint`                                    | in progress (job portable-conventions-lint)                                          |
| Discovery through verbs; the guard steers a crossing search to `magus query`; watchers on real checkouts                       | proposed                                                                             |
| One identity per file: the logical path through the link                                                                       | proposed                                                                             |
| Generated joins per run under `.magus/gen/` for Go, Rust, pnpm, TypeScript and Python; npm, yarn and bun refuse until verified | proposed                                                                             |
| Inert magus-only linters named once per run                                                                                    | proposed                                                                             |
| `describe rules <path>`                                                                                                        | proposed                                                                             |
| Every refusal in decision 10                                                                                                   | not built                                                                            |

### Principles checked

- [No silent defaults](../design/judgment/no-silent-defaults.md): relied on. The skip and
  basename merge become refusals, a waiver names its rule's workspace, an unused
  `//nolint` fails, an inert magus-only linter is named on every run, every generated join
  is announced on every run, and a join pnpm cannot make or an unverified manager refuses.
- [Told, never guessed](../design/judgment/told-never-guessed.md): relied on. A
  connection exists only for roots a person listed; no sibling directory is probed.
- [Everything is a file](../design/principles/everything-is-a-file.md): relied on. The
  connection is a key in one file, and the other workspace is a directory a person can
  `ls`.
- [Delete before you add](../design/principles/delete-before-you-add.md): applied. An
  existing key is reused and refined; `--global`, its qualifier and its env var go; no
  subcommand is added.
- [Every answer has an address](../design/principles/every-answer-has-an-address.md):
  relied on. Keys name link-relative paths and content.
- [No news is good news](../design/principles/no-news-is-good-news.md): relied on. A load
  that changes no link prints nothing, and the freshness header appears only when a
  layer is not fresh.
- [Inform people, contain the engine](../design/principles/inform-people-contain-the-engine.md):
  relied on. Each workspace's own binary builds its store and judges its writes.
- [Built for the largest monorepos](../design/principles/built-for-the-largest-monorepos.md):
  relied on. The other store is read in place, never rebuilt per query; `.magus/` stays
  pruned so no walk crosses. No measurement of a stacked query exists yet.
- [What we do not build is the product](../design/principles/what-we-do-not-build.md):
  no committed connection, no fetching, no versions, no transitive lists.
- [ADR 0014](0014-running-magus-is-the-consent.md): kept. Listing a workspace in one's
  own config is the consent to run its code from the other's targets; read verbs never
  build or evaluate.
- [ADR 0015](0015-a-call-runs-where-its-line-says.md): kept and extended to a call
  touching two trees.
- [ADR 0017](0017-a-cache-hit-stays-in-its-checkout.md): kept. Paths through a link are
  inside the checkout.
- [ADR 0021](0021-a-change-accepts-the-findings-it-causes.md): reused for waivers.
- [A record of refusals](../design/refusals.md): checked; nothing refused there is
  reopened.

## Alternatives

### A committed `use` key with a pinned `ref`, magus.lock and a `replace` override

The previous draft of this record (785f07b48). Lost to Eli's reframe: connecting two
workspaces is a development aid on one machine, and a committed connection makes every
clone and CI depend on it. Reopen it if a workspace must build in CI against another at a
known revision.

### A marker in front of the other workspace's paths (`@magus/`, `@magus//`, `magus:`)

Lost: the link makes a plain path work for every tool, magus's and not, and a marker
works only where magus parses it. The collision analysis of the previous draft is moot.

### Hard links instead of a symlink

Lost on the five reasons in decision 2.

### One union store built by the reading binary

Lost: the reading binary would describe a tree it was not built from, and it writes the
other's `.magus/` from a read verb, as knowledge.go:175-190 does today.

### Rewrite ids with a workspace prefix

Lost: a prefix breaks the about 48 `kind:` parsers, as `--global`'s ids do today. A path
through the link keeps every id's shape.

### Adapt magus's lint config by arguments to the adopting call

Round 6's answer, reversed by round 7: B would pass its module and exceptions to a
composing call, so every consumer restates settings and a new magus rule needs a consumer
change. Portable rules remove the composing step. Reopen it if a rule cannot read its
settings from the linted tree.

### A lint overlay file, a block in `.golangci.yml`, or a per-entry block in magus.yaml

Rejected by Eli in round 5.

### Search through the link

Not chosen: grep, ripgrep, git and `go ./...` skip symlinks and ignored paths by default,
and following the two-way links loops. Discovery goes through magus verbs, which already
cross and label. Reopen it if agent hosts' search follows ignored symlinks by default.

### Realpath as a file's identity

Lost: a realpath names a directory on one machine, so keys would miss across machines,
and Go reports the logical path. The logical path is stable, and realpaths are mapped to
it.

### Keep `--global`

Lost: with read verbs crossing by default there is nothing left for it to do.

### Adopt a workspace's rules by connecting it

Lost: connecting is personal and uncommitted, so rules that changed with it would make
B's findings depend on whose machine ran them. A rule applies only where a committed
magusfile adopts it through the link.

## Consequences

- A person with both checkouts listed sees, edits, queries and lints across them with
  plain paths, and the other side's edits show on the next run.
- A clone or CI without the user config runs every target that does not reach through a
  link, and refuses those that do with the fix named.
- B's committed magusfile can name `.magus/workspaces/magus/...`, a path that exists only
  where magus is connected. That is the one committed trace of the connection, and its
  refusal is explicit.
- An unmounted or deleted listed checkout stops every load on the list until the entry is
  deleted or the path returns.
- `--global`, `MAGUS_KNOWLEDGE_WORKSPACES` and the bare-string entries break; scripts using
  them fail with the refusal that names the new shape. knowledge_test.go:56,86 and
  graph_test.go:39 change.
- grep, ripgrep, find, git and `go ./...` skip the links, so an agent that searches the
  tree sees nothing of the other workspace; the guard steers it to `magus query`.
- A local Go build can pass through a generated go.work that CI lacks; the `GOWORK` line
  on every run is the warning, as the pnpm, tsc and `PYTHONPATH` lines are for theirs.
- A pnpm join keeps its own lock and `node_modules` under `.magus/gen/pnpm/`, so a joined
  install costs a second install, and the editor's tsserver does not see the TypeScript
  join.
- docs/reference/config.md:3 omits the user tier and is corrected with this key's docs.
- No benchmark was run for a stacked query or the link walk.

## Amendments

### 2026-11-06: rewritten around the reframe

The first draft (7bca09646, a619abe45) and the rewrite (785f07b48) designed a committed
`use` key with `ref`, a magus.lock pin, a `replace` override, `share` kinds and an `@name/`
marker. Eli's reframe replaced all of it: the existing `knowledge.workspaces` key in the
user tier connects workspaces both ways, nothing is committed, and each workspace is a
directory symlink under the other's `.magus/workspaces/`. Kept from that draft: the file's
workspace judges a write, the guard loads both policies, rule precedence by name, a stale
narrowing fails, affected crosses, rows carry freshness, the stacked graph read in place,
the weight, the refusal of escaping read patterns and the read-only sandbox entry.

### 2026-11-06: rounds 6 to 8, and the file renamed

Renamed from 0028-use-stitches-workspaces.md. Round 6 settled `affected ci`, the weight,
two checkouts of one repository and the run-time refusal; its lint answer (arguments to
the adopting call) was reversed by round 7, which makes magus's rules portable and leaves
exceptions to `//nolint` at the line. Round 8 adds discovery through verbs, the tools that
skip the link, the logical path as a file's one identity, watchers on real checkouts, the
generated go.work and the pnpm and uv refusal.

### 2026-11-06: where each part lives

Adds the table of core against spells under Eli's layering rule, moves go.work, the pnpm
and uv refusals, Cargo and tool identity into their spells, and names the prototype's
`hack/magusfile/run-golangci.buzz` and `types.ShareKind` as pieces in the wrong layer.

### 2026-11-06: round 9, and an inert-linter finding

Closes the Cargo, guard and realpath questions with Eli's round-9 answers: the Rust spell
passes a generated `[patch]` with `cargo --config`, the guard denies a cross-workspace
search and names the magus command, and realpaths are mapped back by the spells in magus
output only. Records the portable-conventions-lint finding (5fe2bd3e7) that a linter
configured only under `in-module` builds nothing outside magus, silently, and asks Eli how
buzzed's run should say so.

### 2026-11-06: rounds 10 and 11

Round 10 reverses round 8's pnpm and uv refusals: every spell supports a join generated
per run. The verified routes are a pnpmfile with `--global-pnpmfile` and `--lockfile-dir`
under `.magus/gen/pnpm/` (refused under a frozen lockfile), a generated tsconfig run with
`tsc -p` (the spell says tsserver does not see it), and `PYTHONPATH` with `uv run --frozen`.
npm, yarn and bun are unverified and refuse until a join is reproduced. Round 11 settles the
inert-linter question: one line per run naming them, and the run passes.

### 2026-11-06: round 12, and no open questions

Eli took both remaining recommendations: npm, yarn and bun refuse a joined build with
`workspace-join-unverified` until each is reproduced, bun first; a Python join sets
`PYTHONPATH` over the project's own venv, with no venv under `.magus/gen/`. The Open
questions section is removed, since none remain.
