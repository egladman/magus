# CLAUDE.md

magus is a monorepo build/task tool (Go 1.25, module `github.com/egladman/magus`).
Users declare targets in `magusfile.buzz` (Buzz, an embedded scripting language);
magus resolves spells/ops, sandboxes execution, and caches results. This repo
builds itself with magus; see `magusfile.buzz` at the root.

Start with `MAGUS.md`, the generated routing index; do not hand-edit it. `magus
ls` and `magus ls targets <project>` list what exists. How to use magus is not
this file's job. What follows is what only this file can say.

## Which magus binary

Use `./magus`. The released binary on PATH is below this workspace's
`required_version` floor, so it cannot load the tree at all. Build one with
`magus run go-build .`: it regenerates the go:embed'd spell bytecode first, which
a bare link would otherwise bake in stale, and it skips the `format` to
`generate` to `deploy-generate` chain that `build` fails on in a fresh worktree.
An existing `./magus` is fine to keep using; do not rebuild it per command.

Re-check with `magus ls` before assuming either way. This flips back the moment a
release carries the floor, and `magus doctor` reports both halves: `guard-binary`
names which binary a hook would execute and whether it predates the tree,
`required-version-covers-schema` names the keys the floor is protecting.

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
so a plain `affected ci` runs as `ci:rw` and `generate` auto-writes its output
locally, hiding uncommitted-gen drift; `--no-default-charms` strips that so
`generate` acts as the pure drift gate exactly as CI runs it.

CADENCE: once per branch, when the substantive change is complete, before the
first push. After a green gate, a small delta (a comment or changelog edit,
regenerated output, a clean merge of main) needs only the regeneration it touches
(`magus run generate:rw <projects>` plus a settledness pass, and it is one
invocation for many projects); push and let the PR's CI be the gate, since it
runs the identical command on the identical tree. Re-gate locally only when the
delta is engine code or crosses projects. The failure this rule answers: three
agents re-gating changelog-entry deltas serialized twenty minutes apiece on the
machine budget and bought nothing a PR run would not have (2026-09-04, Eli's
call). Regeneration stays local and mandatory: a born-red PR wastes a full CI
round; a red PR from a risk you knowingly deferred is the system working.

A gate run can leave you without `./magus`, because `go-build` declares no
outputs on purpose (`magus describe target go-build .`), so a read-only run never
leaves a fresh binary behind. Rebuild after gating.

## Rules

- No emojis anywhere: code, output, commits, docs.
- User-facing message strings are plain ASCII (no em-dashes, curly quotes);
  code comments are exempt. Docs frontmatter is plain ASCII too.
- Never hand-edit generated files (`gen/` dirs, `MAGUS.md`, `docs/gen/`); change
  the source of truth and regenerate. Generated output lives in a `gen/` dir and
  carries no extra suffix, so the directory is the signal. The one exception is a
  generated METHOD set (`types/buzzobject_gen.go`, `types/enum_gen.go`): Go puts a
  method in its receiver's package, so those files cannot live in a subdirectory
  and carry the `_gen.go` suffix instead.
- Regenerate in the SAME commit as the source change that invalidated the output.
  The `magus-local-development` skill carries this stamped, under "A std/
  descriptor edit is never local", with the evidence and the retire-when.
- Docs site follows classless Pico: semantic HTML, minimal custom classes,
  no inline styles.
- Language-level changes in `libs/gopherbuzz/` must match upstream Buzz behavior.
- Buzz code is tested with in-file `test "..." {}` blocks; run via
  `magus buzz -t <file>`, adding `--embedded` for files written for the magusfile
  engine (parsing is upstream-strict by default, so most Buzz in this tree
  needs it).
- Commits: subject line only, no area prefix, no Co-Authored-By trailer;
  join multiple ideas with semicolons. Never push unless explicitly asked.
- Git is the orchestrator's job: do VCS ops yourself, never delegate git to a
  subagent, and run mutating subagents isolated or serialized. Never a whole-tree
  git op (`stash`/`reset`/`checkout .`/`clean`) to verify a build; it wipes a
  concurrent agent's untracked work. See the magus-vcs-hygiene skill.
- Code that exists ONLY to keep older data, artifacts, or callers working carries a
  `compat(until: <condition>):` comment, in the shape of the existing
  `optimization:` prefix. Three things, or it is not auditable: what it supports,
  the condition that retires it, and how you would OBSERVE that dropping it is
  safe. A date is not a condition; "no store still serves ed25519 envelopes" is.
  Secondary sites say `compat: see <the primary site>` rather than restating it.
  Use Go's `// Deprecated:` instead when the thing is an exported API callers
  should stop using; staticcheck's SA1019 already enforces that one, and it is
  the wrong marker for an internal branch nobody should stop reaching. Do not mark
  code that merely LOOKS like a shim: resolving an old-width output ref is not
  compat, because attempt ids share that shape and there is nothing to rip out.
- `TODO`, `FIXME`, and `BUG` comments are WELCOME and stay. Do not add `godox` (or
  any linter that reports them) to `.golangci.yml`: a gate that fails because a
  note exists is red by design, and banning the note does not do the work. This is
  a standing decision, not an oversight; the `compat(until:)` marker above is the
  one comment convention worth enforcing, and even it is served better by a
  structural check than by keyword matching.
- Before "fixing" behavior that looks wrong, look for the test that pins it. Same
  skill, under "Look for the pinning test before you 'fix' something".

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
- Be terse. Lead with the result; no status narration ("now I'll..."), no
  recap of finished work, no restating a plan before doing it, no selling.
  A mid-task update earns its place only when direction changes or something
  load-bearing surfaced. When a reply runs long, cut detail, never clarity.

## Layout

- `magus.go` + root `*.go`: public API and composition root (`Open`, `Inspect`)
- `types/`: pure domain types; near-leaf. It imports only `spells` and
  `libs/diagnostics` (both deliberate and one-way, see `spells/doc.go`), and
  nothing else in the module. A type a magusfile or a script reads lives HERE, not
  behind an alias in the package that computes it.
- `internal/`: the engine (cache, interp, depgraph, spell, proc, sandbox, guard)
- `cmd/magus`: the CLI; `cmd/magus-*`: codegen and docs tools
- `std/`: the Buzz host modules a magusfile calls (`fs`, `os`, `http`, `vcs`).
  `std/module.go` holds the registry each one registers into. There is no `host/`
  tree any more; it was folded in here, so a reference to `host/gen/` predates that.
- `libs/`: code that versions independently of magus. `libs/gopherbuzz` (the
  embedded Buzz implementation) and `libs/diagnostics` carry their own `go.mod`;
  `libs/textsearch` is part of the main module.
- `spells/`: built-in spell sources (`.buzz`), compiled into the binary
- `docs/`: markdown sources; `docs/render.buzz` renders them into the static site
  at `docs/gen/` (generated, NOT committed; cd.yaml renders it at deploy time)
- `console/`: the native console PWA (standalone pnpm project); read
  `console/README.md` before touching it (CSS naming, PF conventions)

## Local gotchas

- A fresh worktree needs `mise trust` before `magus run lint` will pass. mise keys
  trust on the config file's ABSOLUTE path, so every worktree gets its own
  untrusted `mise.toml`, and the tools it provides (golangci-lint, govulncheck,
  shellcheck) then exit non-zero. The failure names neither mise nor trust; it
  surfaces as `govulncheck exited 1`, so check the run log before believing the
  tool found something. Trust the tree once instead of per worktree:
  `mise settings add trusted_config_paths ~/Repos/magus`.
- `magus run lint .` is GREEN, and `magus affected ci` has no known
  local-environment failure, so treat any failure in the gate as yours rather
  than as background noise.
- Verifying the console locally: the service worker precaches aggressively and
  serves stale bundles. Serve `console/gen` on a fresh port, or unregister the
  SW and clear caches before trusting what you see.
- Leftover `.claude/worktrees/` copies duplicate spell sources and trip MGS1002
  when running magus at the repo root; remove dead worktrees first.
- Run magus from the workspace and NAME the project (`magus run <target>
  <project>`) instead of `cd`-ing to it; a leading `cd` makes every later command
  depend on invisible state, which is why the alternative looks fine. A
  different workspace is `--root <path>`. Running magus inside a temp or
  scratchpad COPY of the tree is refused outright; a pristine tree is a
  throwaway `git worktree`, never a copy.
- Forwarded args are APPENDED to the op's default args, so a package path after
  `--` does NOT scope a run: `magus run go::go-test . -- ./internal/foo/` runs the
  whole tree plus that package. `-- -run 'TestName'` does narrow, because a test
  flag applies to every package. magus flags go BEFORE `--`.
- The daemon (MCP, warm graph, symbol indexing) has no hot reload: after a
  rebuild, `./magus server stop` then `./magus server start` to pick up new code.
  Do not wire a watch-rebuild loop; magus is the task orchestrator, so it would
  rebuild and restart itself mid-run.

## Agent surface

- `.claude/skills/magus-*` are INSTALLED copies (stamped, checked by `magus
  doctor`'s `agent-skills`); edit the sources in `internal/agent/skills/` and
  re-run `magus agent install .claude/skills --force`. `magus-skill-authoring`
  and `magus-local-development` are hand-authored and tracked (pinned by
  `conventions_test.go`'s `handAuthoredSkills`); edit those in place, and read
  magus-skill-authoring before touching the agent surface.
- Record decisions worth keeping (with the why) via the `magus_memory` MCP tool;
  read its status/decisions files at session start.
- Load a skill before acting, not after something breaks. The Skill tool's own
  listing says which covers what; the ones that fire here are magus-vcs-hygiene,
  magus-run, magus-query, magus-docs-lookup, and magus-architecture-review.

  Treat that as necessary and NOT sufficient. Measured 2026-08-24 over one long
  session: the only skills that loaded on their own were the two a hook demanded,
  and `magus-run` was named right here and skipped anyway while `./magus run ...`
  was typed dozens of times. A rule that lives only in prose is a rule with
  roughly even odds. If a convention matters, give it an enforcement point; the
  new-directory advisory in `internal/guard/sourcedir.go` is the worked example.

## Workflows

Seven, in `.github/workflows/`. The name says which, but the trigger does not
follow from it, and the table that used to live here drifted twice. Read the
files; this lists how each job builds magus:

```sh
awk '/^  [a-z][a-z0-9_-]*:$/{j=$1} /source-path:|git-ref:/{print j, $0}' .github/workflows/*.yaml
```

What that cannot show. `ci.yaml` also runs on a main push, and that is not a
publish step: the push run populates the shared cache and the run history a pull
request may only read. `release-index.yaml` and `release.yaml` read
`MAGUS_SIGNING_KEY`; `registry.yaml` signs with a SEPARATE `MAGUS_REGISTRY_KEY`,
because its input is several hundred third-party HTTP responses, the last place
the key that signs magus binaries should be reachable from. Neither pushes to
main, so each opens a PR, and merging it is what publishes.

Checking CI on a pull request: BATCH-POLL, do not watch.

```sh
gh pr list --state open --json number,mergeable,statusCheckRollup
```

`--watch` costs no tokens, but every completion wakes the agent for a turn, and a
third of them race CI startup, report no checks, and need relaunching. Green
changes nothing anyway, since the human merges, so poll when the answer is
needed and reserve `--watch` for a red result you are iterating on.
