# CLAUDE.md

magus is a monorepo build/task tool (Go 1.25, module `github.com/egladman/magus`).
Users declare targets in `magusfile.buzz` (Buzz, an embedded scripting language);
magus resolves spells/ops, sandboxes execution, and caches results. This repo
builds itself with magus.

Start with `MAGUS.md`, the generated routing index. `magus ls` and `magus ls
targets <project>` list what exists.

This file carries only what no guard rule, conventions test, doctor check or
diagnostic tells you. When a tool starts saying something, delete it here.

## Binary and gate

- Use `./magus`, built by `magus run go-build .`. A fresh worktree has none, and
  until it does the hooks run whatever `magus` is on PATH, which may not load this
  tree; `tools/policy/guard.buzz` does not run at all then. Build before relying
  on the guard.
- Never keep running a renamed `./magus`. The hooks find the binary by that name,
  so a rename hands every session in the checkout to the PATH binary, and the guard
  recognizes magus by basename, so the renamed one escapes every magus rule.
  Moving it aside is only a step in relinking.
- The gate is `magus affected ci`. It fails on stale generated output rather than
  rewriting it, so run `magus affected generate:rw` first.

## Rules

- New generated output goes in a `gen/` dir with no suffix. The existing
  exceptions (`MAGUS.md`, docs reference pages, installed skills) are declared
  outputs; `magus describe file <path>` is the authority, not the directory. A
  method set is written by hand (see `types/enums.go`).
- Language-level changes in `libs/gopherbuzz/` must match upstream Buzz behavior.
- Code that exists ONLY to keep older data, artifacts or callers working carries
  `compat(until: <condition>):`, naming what it supports and how you would OBSERVE
  that dropping it is safe. A date is not a condition; "no store still serves
  ed25519 envelopes" is. Secondary sites say `compat: see <primary site>`. An
  exported API callers should leave gets `// Deprecated:` instead.
- `TODO`, `FIXME` and `BUG` comments stay. Never add `godox` or any linter that
  reports them.
- Before "fixing" behavior that looks wrong, look for the test that pins it.
- Docs site follows classless Pico: semantic HTML, minimal custom classes, no
  inline styles.

## Layout

- `magus.go` + root `*.go`: public API and composition root (`Open`, `Inspect`)
- `types/`: pure domain types, near-leaf. A type a magusfile or script reads
  lives here, not behind an alias in the package that computes it.
- `internal/`: the engine (cache, interp, graph, spell, proc, sandbox, guard)
- `cmd/magus`: the CLI; `cmd/magus-*`: codegen and docs tools
- `std/`: the Buzz host modules a magusfile calls, registered in `std/module.go`
- `libs/`: code that versions independently, most with its own `go.mod`
- `spells/`: built-in spell sources (`.buzz`), compiled into the binary
- `docs/`: markdown sources; `docs/render.buzz` renders the site into `docs/gen/`
  (not committed; cd.yaml renders it at deploy time)
- `console/`: the native console PWA (standalone pnpm project); read
  `console/README.md` first
- `tools/policy/guard.buzz`: this repo's own guard rules

## Local gotchas

- Trust the tree once, not per worktree: `mise settings add trusted_config_paths
  ~/Repos/magus`. Untrusted mise config surfaces as `govulncheck exited 1` and
  names neither mise nor trust, so read the run log before believing a finding.
- Forwarded args are APPENDED to the op's defaults, so a package path after `--`
  does NOT scope a run. `-- -run 'TestName'` does narrow. magus flags go BEFORE `--`.
- The daemon (MCP, warm graph, symbol indexing) has no hot reload: after a
  rebuild, `./magus server stop` then `start`. Two builds at one commit share a
  version string, so `magus status` shows no skew for a mid-work rebuild.
- Verifying the console: the service worker precaches and serves stale bundles.
  Serve `console/gen` on a fresh port, or unregister the SW and clear caches.

## Agent surface

- Record decisions worth keeping, with the why, via `magus_memory`.
- If a convention matters, give it an enforcement point; `internal/guard/dir.go`
  is the worked example. Measured 2026-08-24: a rule that lives only in prose has
  roughly even odds.
