# Changelog

All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

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
- Charm conflict warning: when two active charms edit the same argument, one
  silently overrides the other (the winner decided by name order), so magus now
  warns that the losing charm has no effect. Disjoint edits never trip it.

### Changed

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
