# Changelog

All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Knowledge graph gains build and runtime dimensions: a `command` node per concrete
  argv a target runs (grouped by a `tool` node - the program that both commands and
  spells `use`), test `coverage` with a `test_refs` count folded onto file and symbol
  nodes from the coverage profile magus already produces, and `magus refs` now returns
  the definition's `file:line`. Query recipes: [docs/knowledge.md](docs/knowledge.md).
- `daemon.enabled` (flag `--daemon-enabled`, env `MAGUS_DAEMON_ENABLED`, default true):
  set false to run each invocation self-contained in its own per-process pool instead
  of discovering and adopting the shared `magus server start` daemon - handy for a
  worktree that should not touch a shared daemon. Recursive `magus` calls still forward
  over a per-process socket to share the concurrency budget; only the shared daemon is
  opted out of.
- Self-documenting output templates: bare `-o template` (no body) lists the
  command's output fields - the json keys usable in `-o json` and `-o template`,
  with each field's type, drilling into nested types. Works for every structured
  command (the field list is reflected from the output value, no per-type
  registration). Previously an empty template was an error. No new command or
  format: it rides the existing `-o template` surface.
- Spell authoring kit: `magus init spell` scaffolds a spell, `magus buzz -t` runs a
  spell's in-file test blocks, and `magus buzz lsp` serves diagnostics and
  completion to an editor over stdio.
- `buf-breaking` op in the buf spell: gates a proto schema against a baseline
  branch, composable into a `lint` target. See [Breaking changes](docs/breaking-changes.md).
- `describe target --explain` prints the charm trace behind a target's resolved
  command, so a stacked argv patch is inspectable before a run.
- Silent-failure diagnostics: an invalid charm patch (MGS6001), a `has_charm` typo,
  a spell that binds zero ops, and an unknown project name now report a coded,
  actionable error instead of failing quietly.
- Interspersed global flags: `magus <command> --verbose` and `magus --verbose
<command>` now parse the same way.
- `magus describe charm[s]` inverts the charm index: it lists every target that
  declares a charm and the argv edit it makes, marking the reserved built-ins and
  workspace defaults.
- Charm conflict detection: when two active charms edit the same argument, one
  silently overrides the other (the winner decided by name order), so magus warns
  that the losing charm has no effect at run time and flags it in `magus describe
target ...:a,b` before a run. Disjoint edits never trip it.
- `magus describe target` describes a service op before it runs: its readiness
  probe, stop command, idle window, whether it is shared, and its dedup fingerprint.
- `magus graph` is the home of the workspace's graphs as objects: `graph deps`
  emits the project dependency DAG (the standalone form of `run --graph` /
  `affected --graph`, which remain), `graph export` emits the merged knowledge
  graph (`-o json` node-link, or the new `-o graphml` for external graph
  viewers), and `graph stats` reports its shape (god nodes, orphans, doc
  coverage; `--kind` to scope). The `query`/`explain`/`path` retrieval verbs
  are unchanged.

### Fixed

- Forwarding to a daemon of a different build no longer warns. A version/protocol
  mismatch means the daemon is alive but will not adopt a mismatched client, so the
  command now falls back to local execution quietly (a debug line, not a `[warn]
  proc forward failed` line). This is routine when multiple worktrees run different
  builds against one shared per-user daemon.
- A workspace-local Buzz spell could not declare a service op: the host-registered
  `magus/target` module omitted the `Service` type (present only on the dry-run
  host), so `Service{...}` failed to compile. Both hosts now register it.

### Changed

- Breaking: `-o template=<go-template>` now renders against the JSON-normalized
  value, so template field names are the json-tag keys (`{{range .projects}}{{.path}}{{end}}`),
  identical to what `-o json` emits, instead of the PascalCase Go struct fields
  (`{{.Projects}}`/`{{.Path}}`) it exposed before. This makes `-o json` a faithful
  reference for authoring templates. Numbers arrive as float64 (coerce with `int`
  before numeric comparison); `join` now accepts any list, not just `[]string`.
- Breaking: `magus describe knowledge` is now `magus graph export`, and
  `magus insight structure` is now `magus graph stats`; the old spellings error
  with a pointer to the new home. `insight report` still embeds the graph-stats
  section, renamed from `structure` to `graph_stats` in its `-o json`/`yaml`
  output (the `KnowledgeStats` schema itself is unchanged).
- `magus buzz lsp` replaces the top-level `magus lsp`.
- Local spell imports resolve workspace-root-first with walk-up accrual; a name
  collision between an ancestor and a descendant spell is flagged (MGS1002) and
  suppressed only with an acknowledged `spells.allow_shadow` reason.

## [v0.1.0] - 2026-07-05

### Added

- Playground: an in-browser CodeMirror editor with live diagnostics, module and
  symbol autocompletion, hover docs, and call-signature help, backed by the
  WebAssembly interpreter; a collapsible notice lists the host modules the
  browser cannot run.
- Docs site: first-class `/blog` subsystem with reverse-chronological listing,
  breadcrumb root, per-post edit links, and Blog nav item.
- Docs site: two Atom 1.0 feeds — `/public/atom/blog.atom.xml` (posts) and
  `/public/atom/releases.atom.xml` (releases, derived from this file).
- Docs site: nested Apache-`mod_autoindex`-styled `/public/` tree with an
  autoindex helper — hub at `/public/`, feeds at `/public/atom/`, release
  artifacts at `/public/release/`.

### Changed

- Docs site: extensionless URLs everywhere (`/documentation/`, `/modules/fs/`);
  the authored `docs/manpage/gen/` path segment is flattened out of public URLs.
- Docs site: nine flat client scripts collapsed into a two-file esbuild bundle
  (`theme.js` head-critical, `main.js` deferred module).
- Docs site: nav link "GitHub" moved to the footer, relabeled "Source Code".

### Fixed

- Docs site: mobile TOC becomes a slide-up bottom-sheet instead of stacking
  above the article; page toolbar reflows so search fills its row and
  "Suggest an edit" drops below.
