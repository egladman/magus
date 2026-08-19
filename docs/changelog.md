---
title: Changelog
generated_from: CHANGELOG.md
description: Every released change to magus, newest first, in Keep a Changelog format. Generated from releases/*.yaml.
tags: [changelog, releases, versions, upgrade, breaking-changes]
---

# Changelog

All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

See the unreleased changes at
https://github.com/egladman/magus/compare/v0.4.0...main

### Changed

- Agent guard templates are at version 7. Codex users must also add
  `GUARD_NO_ADVISE=1` to both hook commands in `hooks.json`; re-downloading the
  templates changes nothing on its own. Other hosts render as before.

### Fixed

- `magus vcs resolve` no longer gives up when the merge left conflict markers in
  a magusfile. It reads the committed magusfile through the new
  `types.RevisionFileReader` capability, settles the generated conflicts with
  those declarations, and leaves the hand-written one for you - rather than
  reporting the interpreter's verdict on a `<<<<<<<` line and settling nothing.
  It deliberately does NOT regenerate in that state: a merge that changes a
  generator would produce bytes matching neither side, so it says to run
  `magus run generate:rw` once the magusfile is resolved.
- `magus vcs resolve` no longer loses the whole resolution to one renamed path.
  It staged with a single call whose pathspecs included files the rename had
  removed, and one pathspec matching nothing aborts the call before staging
  anything - so regeneration completed and the index was left untouched. Paths
  that cannot be staged are now reported by name instead.
- `docs:site-generate` declares the bundles it needs rather than relying on
  `generate` to order them first. It was correct as a stage and broken alone, so
  `magus run site-generate docs` - and `magus vcs resolve`, which invokes the
  target owning a conflicted output directly - failed the site's asset-integrity
  check on the playground's missing wasm glue.
- Agent guard: Codex is no longer sent advisories it rejects. Its PreToolUse
  treats `additionalContext` as an error and then fails open, so each advisory
  was discarded and disarmed the guard for that call. It now declares
  `advise=none`. The two fail-open notices still carry the key.
- Agent guard: the `relock` charm is now taught in the magus-run skill, and a
  gate keeps every advisory covered there. An advise injects on one host of
  four, so guidance carried only by a verdict never reached the rest.

## [v0.3.0] - 2026-07-25

See the full changelog at
https://github.com/egladman/magus/compare/v0.2.1...v0.3.0

## [v0.2.1] - 2026-07-19

See the full changelog at
https://github.com/egladman/magus/compare/v0.2.0...v0.2.1

## [v0.2.0] - 2026-07-18

See the full changelog at
https://github.com/egladman/magus/compare/v0.1.0...v0.2.0

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
