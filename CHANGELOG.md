# Changelog

All notable changes to this project will be documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

See the unreleased changes at
https://github.com/egladman/magus/compare/v0.4.0...main

### Removed

- The four JSON run-browser routes are gone: `GET /api/v1/outputs`,
  `/api/v1/output?ref=`, `/api/v1/runs` and `/api/v1/run?inv=`. The typed
  `magus.viewer.v1alpha1.ViewerService` replaces them - `ListOutputs`,
  `GetOutput`, `ListInvocations` and `GetJournal` read the same two stores, and
  the contract they speak is generated rather than hand-marshaled. The service
  had been defined since the log viewer shipped and was never mounted; the JSON
  routes were hand-written strings with no schema behind them.
- `magus.FailOnDrift()` is gone, with no replacement alias. It named the
  response rather than the decision, so it could not carry "warn" or "off", and
  what it checked - whether the working tree was dirty after the target - was
  disarmed by any unrelated uncommitted edit. Use `magus.Drift(policy, reason)`,
  or the `drift` key in a magusfile's target policy.

### Changed

- The drift gate runs for **every** target that declares outputs, and no
  declaration turns it on. Declaring an output is already the claim that those
  bytes follow from the target's inputs, so magus checks the claim rather than
  asking each workspace to opt in. It previously applied only to `preflight` and
  `generate`, and only when a target declared `FailOnDrift`. Turn it down with
  `{"drift": "warn"}` or off with `{"drift": "off", "drift_reason": "..."}`; a
  reason is required to switch it off, as it is for `skip_cache`.
- The drift gate fails only for output **this change** made stale. Output that
  drifted with no source change behind it - a merge whose own CI never finished,
  a generator nobody re-ran - is reported and does not fail the run. Failing it
  billed whoever opened the next pull request for a decision they were not party
  to, and they could not fix it without committing bytes they did not produce.
  This is not configurable: there is no setting that restores the old behavior.
- The gate now hashes each target's declared outputs before and after the run,
  rather than asking the VCS what is dirty afterwards. Bytes that did not move
  are not drift however dirty the surrounding tree is, and detection no longer
  depends on the VCS at all - only attribution does.
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
