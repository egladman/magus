# CLAUDE.md

magus is a monorepo build/task tool (Go 1.25, module `github.com/egladman/magus`).
Users declare targets in `magusfile.buzz` (Buzz, an embedded scripting language);
magus resolves spells/ops, sandboxes execution, and caches results. This repo
builds itself with magus.

Start with `MAGUS.md`, the generated routing index. `magus ls` and `magus ls
targets <project>` list what exists.

This file carries only what nothing else can tell you. A rule the guard, a
conventions test, or a diagnostic already enforces is NOT restated here: the
first denial teaches it, and prose that repeats a deny is prose nobody reads.

## Which magus binary

Use `./magus`. The released binary on PATH is below this workspace's
`required_version` floor, so it cannot load the tree at all. Build one with
`magus run go-build .`, which regenerates the go:embed'd spell bytecode a bare
link would bake in stale. Keep an existing `./magus`; do not rebuild per command.
`magus doctor` reports both halves (`guard-binary`, `required-version-covers-schema`),
and this flips back the moment a release carries the floor.

A gate run can leave you without `./magus`: `go-build` declares no outputs on
purpose, so a read-only run leaves no binary behind. Rebuild after gating.

Bootstrap deadlock: change the magusfile schema, or pull a change adding a type
the spell runtime provides, and every magus command fails at workspace load,
including the one that would build the binary that understands it. Escape by
shelving just that hunk (`git stash push -- <file>`) or restoring the pre-pull
spell sources long enough to build. Only the spells the root magusfile IMPORTS
have to be shelved; a local spell that fails to load is logged and skipped.

NEVER run, copy, or link another worktree's `./magus`. It was linked from that
tree's sources, so its verdicts describe a tree that exists nowhere, and what it
regenerates lands here unmarked. Nothing enforces this.

## The gate

`magus affected ci --no-default-charms`. `magus.yaml` sets `default_charms: [rw]`,
so a plain `affected ci` lets `generate` auto-write locally and hide uncommitted
drift; stripping the charm makes it the pure drift gate CI runs.

CADENCE: once per branch, when the substantive change is complete, before the
first push. After a green gate, a small delta needs only the regeneration it
touches (`magus run generate:rw <projects>`, one invocation for many projects);
push and let the PR's CI be the gate, since it runs the identical command on the
identical tree. Re-gate locally only when the delta is engine code or crosses
projects. Regeneration stays local and mandatory: a born-red PR wastes a full CI
round, while a red PR from a risk you knowingly deferred is the system working.

## Rules

- Regenerate in the SAME commit as the source change that invalidated the output.
- Generated output lives in a `gen/` dir and carries no suffix, so the directory
  is the signal. No exceptions: Go forces methods into their receiver's package, so
  a method set is written by hand, never generated (see `types/enums.go`).
- Language-level changes in `libs/gopherbuzz/` must match upstream Buzz behavior.
- Buzz is tested with in-file `test "..." {}` blocks via `magus buzz -t <file>`,
  adding `--embedded` for files written for the magusfile engine (parsing is
  upstream-strict by default, so most Buzz here needs it).
- Git is the orchestrator's job: do VCS ops yourself and never delegate git to a
  subagent. Run mutating subagents isolated or serialized.
- Every spawn NAMES its model. Inheriting the parent's on purpose is fine;
  inheriting because nobody said is not. There is no ordering rule to fall back
  on, since same-strength offload is legitimate, so ASK when it is unclear. A
  subagent needs a `./magus` built from ITS OWN worktree before it can validate.
- Code that exists ONLY to keep older data, artifacts, or callers working carries
  a `compat(until: <condition>):` comment naming what it supports, the condition
  that retires it, and how you would OBSERVE that dropping it is safe. A date is
  not a condition; "no store still serves ed25519 envelopes" is. Secondary sites
  say `compat: see <the primary site>`. Use Go's `// Deprecated:` instead for an
  exported API callers should stop using. Do not mark code that merely LOOKS like
  a shim.
- `TODO`, `FIXME`, and `BUG` comments are WELCOME and stay. Never add `godox` or
  any linter that reports them: a gate red because a note exists does not do the
  work. Standing decision, not an oversight.
- Before "fixing" behavior that looks wrong, look for the test that pins it.
- Docs site follows classless Pico: semantic HTML, minimal custom classes, no
  inline styles.

## Layout

- `magus.go` + root `*.go`: public API and composition root (`Open`, `Inspect`)
- `types/`: pure domain types; near-leaf. Of magus it imports `spells`,
  `libs/diagnostics` (deliberate and one-way, see `spells/doc.go`), `internal/json`
  and its own leaf `types/enum`, and it reaches no filesystem, process or network.
  `TestTypesStaysPureDomain` enforces that, so this line is a signpost rather
  than the rule. A type a magusfile or script reads lives HERE, not behind an
  alias in the package that computes it.
- `internal/`: the engine (cache, interp, depgraph, spell, proc, sandbox, guard)
- `cmd/magus`: the CLI; `cmd/magus-*`: codegen and docs tools
- `std/`: the Buzz host modules a magusfile calls, registered into
  `std/module.go`. There is no `host/` tree any more, so a reference to
  `host/gen/` predates that.
- `libs/`: code that versions independently. `libs/gopherbuzz` and
  `libs/diagnostics` carry their own `go.mod`; `libs/textsearch` does not.
- `spells/`: built-in spell sources (`.buzz`), compiled into the binary
- `docs/`: markdown sources; `docs/render.buzz` renders the static site into
  `docs/gen/` (generated, NOT committed; cd.yaml renders it at deploy time)
- `console/`: the native console PWA (standalone pnpm project); read
  `console/README.md` first (CSS naming, PF conventions)

## Local gotchas

- Trust the tree once, not per worktree: `mise settings add trusted_config_paths
  ~/Repos/magus`. Untrusted mise config surfaces as `govulncheck exited 1` and
  names neither mise nor trust, so read the run log before believing a finding.
- `magus run lint .` is GREEN and `magus affected ci` has no known
  local-environment failure. Treat a gate failure as yours, not as noise.
- Forwarded args are APPENDED to the op's defaults, so a package path after `--`
  does NOT scope a run. `-- -run 'TestName'` does narrow. magus flags go BEFORE `--`.
- The daemon (MCP, warm graph, symbol indexing) has no hot reload: after a
  rebuild, `./magus server stop` then `start`. Do not wire a watch-rebuild loop.
- Leftover `.claude/worktrees/` copies duplicate spell sources and trip MGS1002
  at the repo root; remove dead worktrees first.
- Verifying the console: the service worker precaches and serves stale bundles.
  Serve `console/gen` on a fresh port, or unregister the SW and clear caches.

## Agent surface

- `.claude/skills/magus-*` are INSTALLED copies (stamped, checked by `magus
  doctor`'s `agent-skills`); edit the sources in `internal/agent/skills/` and
  re-run `magus agent install .claude/skills --force`. `magus-skill-authoring`,
  `magus-local-development` and `land-pull-requests` are hand-authored and
  tracked (pinned by `conventions_test.go`'s `handAuthoredSkills`); edit those
  in place, and read magus-skill-authoring before touching the agent surface.
- Record decisions worth keeping, with the why, via `magus_memory`.
- If a convention matters, give it an enforcement point. Measured 2026-08-24: the
  only skills that loaded on their own were the two a hook demanded, and a rule
  that lives only in prose has roughly even odds. `internal/guard/dir.go` is the
  worked example.
