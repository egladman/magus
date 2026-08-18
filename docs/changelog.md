---
title: Changelog
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

- Agent guard templates are at version 7; `magus doctor` reports a copy older
  than that. CODEX USERS MUST ALSO EDIT THEIR WIRING - the fix lives in
  `hooks.json`, not in the template, so re-downloading the scripts alone changes
  nothing. Add `GUARD_NO_ADVISE=1` to both hook commands, as
  `docs/guides/integrations/agents/codex.md` now shows. Every other host needs
  only the re-download, and its rendered response is byte-identical to before.

### Fixed

- Agent guard: the `relock` advisory reached the model on Claude Code only. An
  advise is injectable there and nowhere else - Codex rejects the key, Cursor's
  command surface has no channel, OpenCode can only log it for the person - and
  the guidance was in no installed skill, so three hosts of four never learned
  that rewriting dependency state needs the `relock` charm. It is now in the
  magus-run skill, which every host reads, and a gate keeps every advisory
  covered there.
- Agent guard: Codex is no longer sent advisories it rejects. Its PreToolUse
  treats `additionalContext` as an error and then fails OPEN, so every advisory
  magus sent it was discarded AND disarmed the guard for that call, on both the
  command and file surfaces. Codex now declares `advise=none`. The two fail-open
  NOTICES ("magus guard is NOT running") still carry the key and so still do not
  reach a Codex reader; that is unchanged and remains a gap.

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
