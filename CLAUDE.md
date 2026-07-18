# CLAUDE.md

magus is a monorepo build/task tool (Go 1.25, module `github.com/egladman/magus`).
Users declare targets in `magusfile.buzz` (Buzz, an embedded scripting language);
magus resolves spells/ops, sandboxes execution, and caches results. This repo
builds itself with magus - see `magusfile.buzz` at the root.

Start with `MAGUS.md`: the generated routing index and entry point. Do not
hand-edit it.

## Agent surface

- `.claude/skills/magus-*` are INSTALLED copies (stamped, checked by
  `magus graph verify`); edit the sources in `cmd/magus/skills/` and re-run
  `magus agent install claude --force`. Exception: `magus-skill-authoring`
  is hand-authored - read it before touching the agent surface.
- Record decisions worth keeping (with the why) via the `magus_memory` MCP
  tool; read its status/decisions files at session start.
- `.claude/settings.json` + `.claude/hooks/` are committed automation: a
  SessionStart bootstrap (mise, skill freshness, daemon) and a PreToolUse
  nudge when a raw build/test tool is about to bypass a magus target.

## Commands

- Build: `magus run build`
- Test: `magus run test` (or `go test ./...`)
- Lint + vet + vuln: `magus run lint`
- Final gate before handing work back: `magus affected ci` - runs the full
  pipeline (lint, build, test, coverage) over affected projects.
- Regenerate generated files: `magus run generate`, then commit.
- Charm note: `magus.yaml` sets `default_charms: [rw]`, so local runs get
  the `rw` charm automatically - `generate` writes locally. CI strips it
  (`--no-default-charms` / the ci anchor), where `generate` acts as a pure
  drift gate instead.

## Which magus binary

Locally, run HEAD: `go build -o /tmp/magus ./cmd/magus` once per session and
use that (or `go run ./cmd/magus <cmd>` for a one-off). Do not expect a release
binary on PATH; the SessionStart hook falls back to `go run` the same way.

The compatibility contract lives in CI: `setup-magus` runs the pinned,
checksum-verified release against this repo's magusfile. If `magusfile.buzz`
ever needs an unreleased magus feature, that is a breaking-change signal to
surface (release first), not to paper over.

## Running the daemon locally

The daemon is the long-lived process that serves MCP, keeps the knowledge graph
warm, and runs background jobs (symbol auto-indexing). Start/stop it with:

- `magus server start` auto-backgrounds by default: it detaches the daemon, waits
  until it is accepting, prints the pid, and returns 0 (starting when one is already
  running is a no-op that also returns 0, so scripts can chain on it). `magus server
  stop` stops it and prints what it stopped, exiting non-zero when it found nothing
  to stop. The MCP server and the Dashboard come up alongside the daemon. Detached
  daemon logs go to `<sockdir>/magus-daemon.log`. Use `--foreground` (for a
  supervisor like systemd --user, or when debugging) to run it blocking in the
  current process instead.
- Iterating on daemon code: `go run ./cmd/magus server start --foreground` runs HEAD
  in the foreground, but the process is long-lived, so a source edit does NOT take
  effect until you stop and restart it: `magus server stop && go run ./cmd/magus
  server start --foreground`. There is no hot reload.

Do NOT wire a watch-rebuild loop for magus itself. magus is the task
orchestrator, so a "rebuild on every file change" loop would have the tool
rebuilding and restarting itself mid-run - it fights itself and thrashes. Rebuild
deliberately instead:

- One-off HEAD check: `go run ./cmd/magus <cmd>` (compiles fresh each invocation;
  fine for a single command, slow as a loop).
- Exercising a change repeatedly: `go build -o /tmp/magus ./cmd/magus` once, then
  run `/tmp/magus ...`; rebuild when you change the code, not when any file moves.
- The daemon: restart it (stop + start) after a rebuild to pick up new code.

## Layout

- `magus.go` + root `*.go` - public API and composition root (`Open`, `Inspect`)
- `types/` - pure domain types, stdlib-only; keep it a leaf
- `internal/` - the engine (cache, interp, depgraph, spell, proc, sandbox, ...)
- `cmd/magus` - the CLI; `cmd/magus-*` - codegen and docs tools
- `std/` + `host/` - Buzz stdlib modules; `host/gen/` is generated from `std/`
- `gopherbuzz/` - the embedded Buzz language implementation
- `spells/` - built-in spell sources (`.buzz`), compiled into the binary
- `docs/` - markdown sources; `docs/render.buzz` renders them into the
  committed static site at `docs/gen/`
- `console/` - the native console PWA (standalone pnpm project); read
  `console/PATTERNFLY.md` before touching it (CSS naming, PF conventions)

## Local gotchas

- Verifying the console locally: the service worker precaches aggressively and
  serves stale bundles. Serve `console/gen` on a fresh port, or unregister the
  SW and clear caches before trusting what you see.
- Leftover `.claude/worktrees/` copies duplicate spell sources and trip
  MGS1002 when running magus at the repo root; remove dead worktrees first.
- `magus affected ci` has known local-environment failures that are NOT your
  change: the doctor console check needs a running daemon, and there are
  pre-existing lint findings. Trust build and test; compare lint against a
  stash if unsure.

## Rules

- No emojis anywhere: code, output, commits, docs.
- User-facing message strings are plain ASCII (no em-dashes, curly quotes);
  code comments are exempt. Docs frontmatter is plain ASCII too.
- Never hand-edit generated files (`gen/` dirs, `*.gen.buzz`, `MAGUS.md`,
  `docs/gen/`); change the source of truth and regenerate.
- Docs site follows classless Pico: semantic HTML, minimal custom classes,
  no inline styles.
- Language-level changes in `gopherbuzz/` must match upstream Buzz behavior.
- Buzz code is tested with in-file `test "..." {}` blocks; run via
  `magus buzz -t <file>`.
- Commits: subject line only, no area prefix, no Co-Authored-By trailer;
  join multiple ideas with semicolons. Never push unless explicitly asked.

## Working style

- State your assumptions before implementing. If the request has several
  readings, present them instead of picking one; if a simpler approach exists,
  say so and push back when warranted; if something is unclear, ask first.
- Write the minimum code that solves the problem: no speculative features, no
  abstractions for single-use code, no configurability nobody asked for, no
  error handling for cases that cannot happen.
- Touch only what the request requires and match the surrounding style. Do not
  improve or refactor adjacent code; mention unrelated dead code instead of
  deleting it. Do remove imports and helpers your own change orphaned. Every
  changed line should trace back to the request.
- Turn tasks into verifiable goals: a bug fix starts with a test that
  reproduces it; for multi-step work, state a short plan with a check per step
  and loop until the checks pass.
- Lead with the command, path, or snippet; explanation after, no preamble or
  recap. Raise one issue at a time. Keep estimates concrete.
