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
  `magus agent install .claude/skills --force`. Exception: `magus-skill-authoring`
  is hand-authored - read it before touching the agent surface.
- Record decisions worth keeping (with the why) via the `magus_memory` MCP
  tool; read its status/decisions files at session start.
- Invoke the Skill tool at these moments, before acting - not after something
  breaks: `git commit`/`git add`/`git stash`/`git reset` or reading a diff ->
  Skill(magus-vcs-hygiene); about to build/test/lint/generate -> Skill(magus-run);
  "what exists / depends on / uses X" -> Skill(magus-query); "how does magus
  X work" -> Skill(magus-docs-lookup).

## Commands

How to use magus is not this file's job: `MAGUS.md` routes every question, and
`magus ls` / `magus ls targets <project>` list what exists. Two repo policies
that are not derivable from either:

- Final gate before committing (and especially before pushing): `magus affected ci
--no-default-charms`. `magus.yaml` sets `default_charms: [rw]`, so a plain
  `affected ci` runs as `ci:rw` and `generate` auto-writes its output locally,
  hiding uncommitted-gen drift; `--no-default-charms` strips that so `generate`
  acts as the pure drift gate exactly as CI runs it.

## Which magus binary

RIGHT NOW the released `magus` on PATH CANNOT load this workspace. The root magusfile
imports `./tools/toolchain`, which imports the `proc` module no release carries yet, so
every command fails at workspace load with:

```text
magus: workspace://.: magusfile: exec magusfile.buzz: [BZZ2001] buzz: import "./tools/toolchain": [BZZ2001] buzz: import "proc": module not found
```

So build one - `magus run go_build .` - and use `./magus`. This is a temporary state that
ends when the next release ships; re-check it by running `magus ls` before assuming
either way, because the guidance flips back the moment a release carries the key.

The DEFAULT, whenever the PATH binary can load the tree, is to use it and NOT build.
Building is the exception, and it needs a reason:

- a `magus.project` option, target policy, or other magusfile schema change (the
  released binary rejects a key it does not know, and then no magus command can
  even load the workspace - which is the situation above)
- engine, daemon, spell-runtime, or CLI behaviour you are about to run
- a doctor check whose output you want to see against this tree

Editing docs, workflows, or Go you are only unit-testing (`go test ./...` needs
no magus binary) requires NO rebuild. The instinct to build first is expensive in
a way that is invisible locally: the build stamps
`-X main.version/commit/buildDate` from `git describe` and the commit hash, so the
LINK step is unshareable across worktrees and across commits within a worktree.
Roughly 34 worktrees each rebuilding grew `~/Library/Caches/go-build` to 62 GB.
Reclaim with `go clean -cache`.

When you do need one: `magus run go_build .` writes `./magus`. It regenerates the compiled
built-in spells first (they are go:embed'd, so a link before that step bakes in stale
bytecode - about 11s), and unlike
`magus run build .` it skips the `format` -> `generate` -> `deploy-generate`
chain that fails in a fresh worktree on a missing `docs/gen/index.html`. Then run
`./magus <cmd>`. An existing `./magus` newer than the tree (`magus doctor`'s
"guard binary" check) is fine to keep using; do not rebuild it per command.

NEVER run, copy, or link another worktree's `./magus`: it was linked from that tree's
sources, so its verdicts describe a tree that exists nowhere and what it regenerates
lands here unmarked. Nothing enforces this. The trusted spellings are `./magus` and
bare `magus`; if neither loads the workspace, use the two-hop below or ask.

Bootstrap deadlock: after a magusfile schema change, EVERY magus command fails at
workspace load, including the one that would build the binary that understands it.
Escape by shelving just that hunk - `git stash push -- <file>`, `magus run
go_build .`, `git stash pop`.

The same deadlock has a SECOND shape, and `git stash` does not fix it: pulling a
change that adds a type the spell runtime provides (`Secret`, 2026-08-12). The spells
now reference a name your binary lacks, the magusfile fails importing them, and
stashing does nothing because the tree is already correct - it is the BINARY that is
behind. Escape by putting the pre-pull spell sources back just long enough to build:

```sh
git show <pre-pull-ref>:spells/github/actions/spell.buzz > spells/github/actions/spell.buzz
magus run go_build .
git checkout HEAD -- spells/github/actions/spell.buzz
```

Only the spells the root magusfile IMPORTS have to be shelved; a local spell that
fails to load is logged and skipped, not fatal. As of MGS1021's stale-binary
explainer the error now says this is what is happening, rather than reading as a
typo - but it still cannot build the binary for you.

Note `magus affected ci --no-default-charms` can leave you without `./magus`; rebuild
after gating. NOT because it is a declared output - `go-build` deliberately declares
none (`magus describe target go-build .` shows `sources:` and no `outputs:`), so the
binary is never cached as an artifact and never restored from one. `ctx.writesFiles` is
omitted on purpose: the declared-output globs are what EnsureMergeDriver writes into
`.gitattributes`, and `/magus` is gitignored, so declaring it would name a path that can
never be tracked and so can never conflict. The relevant part is just that a read-only
run does not leave a fresh binary behind.

BOTH raw Go entry points are DENIED by the agent guard, verified 2026-08-14
against this tree. `.claude/settings.json` is now TRACKED - `dogfood_test.go`
fails when it is missing, because a skip meant the one test covering this repo's
own guard wiring had effectively never run - so a fresh worktree DOES carry the
wiring. (This file used to say the opposite, from when the config was gitignored:
that a fresh worktree had no guard at all.)

Still enforce the list yourself. The wiring being present is not the same as the
guard running: it resolves `./magus` and then PATH, and a binary that is missing
or too old to judge fails OPEN with a notice rather than blocking. Measured
2026-08-11, back when the config was per-developer: three agents ran `go build`
in a guardless worktree and it succeeded; the rule held only because they chose
to obey it and said so.

- `go build` at every output path, including the `-o /tmp/magus` form this file
  once recommended.
- `go run ./cmd/magus <cmd>`, which this file once recommended as the way to run
  HEAD. It is not an exception; the guard reads the command being RUN, so a
  wrapper, an env prefix, or `bash -c` all reach the same verdict.

Producing a binary is a write, and writes go through magus - the toolchain verb
is what has to change, not the destination. `magus run go_build .` writes
`./magus`; prefer top-level targets over `spell::op` forms, which exist for
passing arguments through to the underlying tool, not as an everyday spelling.

`magus run test .` passes. It previously failed to link with `fingerprint
mismatch: github.com/egladman/magus ...`, which was mise setting
`GOEXPERIMENT=jsonv2` for this repo while a sandboxed child did not inherit it -
two differently-fingerprinted builds of the same package in one cache. The
`GO*` passthrough in `magus.yaml`'s `sandbox.env` was recorded as the fix.

VERIFY BEFORE TRUSTING THAT, because it does not hold today: `sandbox.enabled`
defaults to FALSE, `magus.yaml` never sets it, and there is no user-global config
or `MAGUS_SANDBOX_ENABLED` on this machine. With the sandbox off no policy is
attached (`run.go`'s `if m.cfg.Sandbox.Enabled`), and `childEnv` then hands the
child `os.Environ()` unscrubbed - measured 2026-08-09 with a throwaway workspace:
both `MYVAR` and `GOEXPERIMENT` reached the subprocess intact. So the passthrough
is INERT as configured, and whatever fixed the fingerprint mismatch, it was not
that entry doing the work described here. Either the sandbox was on when this was
diagnosed and has since been off, or the cause was something else and is still
present. Do not delete the passthrough on the strength of this note - it becomes
load-bearing the moment the sandbox is enabled - but do not credit it either.

Flag placement matters when forwarding: magus flags go BEFORE `--`.
`magus run go::go-test . --silent -- ./internal/foo/` works; putting `--silent`
after `--` forwards it to the test binary, which rejects it.

Six workflows, one trigger each, and the name says which:

| File                 | Runs on                        | Ships                                                     |
| -------------------- | ------------------------------ | --------------------------------------------------------- |
| `ci.yaml`            | pull request, and main push    | nothing                                                   |
| `cd.yaml`            | main push                      | docs site (Pages), per-commit container image (GHCR)      |
| `release.yaml`       | `v*` tag                       | binaries and release images                               |
| `release-index.yaml` | manual                         | a PR carrying the signed `public/release/index.json` pair |
| `audit.yaml`         | cron 05:17 UTC Mondays, manual | nothing                                                   |

ci also runs on a main push, and that is not a publish step: the push run is what
populates the shared cache and the run history a pull request may only read.

release-index.yaml and release.yaml's tail both call `.github/actions/release-index`,
and they are the only two things that read `MAGUS_SIGNING_KEY` besides `release-sign`.
registry.yaml signs too, with a SEPARATE `MAGUS_REGISTRY_KEY`: its input is several
hundred third-party HTTP responses, which is the last place the key that signs magus
binaries should be reachable from.
Neither pushes to main - the ruleset requires a pull request and bypasses only for the
admin role - so each opens one instead, and merging it is what publishes the index.

`setup-magus` is called two ways, on purpose:

- `source-path: .` - nearly everything: ci's `preflight`, `ci`, `advice`,
  `report`, both cd jobs, and audit's `determinism`, `toolchain` and
  `skill-evals`. Builds the magus
  THIS commit defines and runs it against this commit's magusfile, so a change that
  `magusfile.buzz` needs is exercised by the very run that introduces it - there is
  no "release first" chicken-and-egg.
- `git-ref: <latest release tag>` - exactly ONE job, audit's `compat`. It runs the
  pinned, checksum-verified release instead. That is the compatibility contract: when
  it breaks because the magusfile needs an unreleased feature, that is a
  breaking-change signal to surface, not to paper over. It is non-blocking by trigger
  now rather than by `continue-on-error` - it is not on the PR path at all - which is
  why it being red on `no_language` (added in 6e087567) is fine. Every release.yaml
  job also pins a ref, but those run on a tag.

Verify rather than trust this list - it has drifted before:
`awk '/^  [a-z][a-z0-9_-]*:$/{j=$1} /source-path:|git-ref:/{print j, $0}' .github/workflows/*.yaml`

Do NOT write `git-ref: ${{ github.sha }}` for the first case. On a
`pull_request` event `github.sha` is the ephemeral `refs/pull/N/merge` commit, a
DESCENDANT of main, which the action's reachable-from-main gate rejects every
time (it reads as "Refusing to build unreviewed source", which is a misdirect).
`source-path` is the input for building a checkout you already have.

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
- Exercising a change repeatedly: `magus run build .` once, then run `./magus ...`;
  rebuild when you change the code, not when any file moves. Blocked on MGS3001
  right now - see "Which magus binary" above.
- The daemon: restart it (stop + start) after a rebuild to pick up new code.

## Layout

- `magus.go` + root `*.go` - public API and composition root (`Open`, `Inspect`)
- `types/` - pure domain types; near-leaf. It imports only `spells` and
  `libs/diagnostics` (both deliberate and one-way - see `spells/doc.go`), and
  nothing else in the module. A type a magusfile or a script reads lives HERE, not
  behind an alias in the package that computes it.
- `internal/` - the engine (cache, interp, depgraph, spell, proc, sandbox, ...)
- `cmd/magus` - the CLI; `cmd/magus-*` - codegen and docs tools
- `std/` - the Buzz host modules a magusfile calls (`fs`, `os`, `http`, `vcs`, ...).
  `std/module.go` holds the registry each one registers into. There is no `host/`
  tree any more; it was folded in here, so a reference to `host/gen/` predates that.
- `libs/` - code that versions independently of magus. `libs/gopherbuzz` (the
  embedded Buzz implementation) and `libs/diagnostics` carry their own `go.mod`;
  `libs/textsearch` is part of the main module.
- `spells/` - built-in spell sources (`.buzz`), compiled into the binary
- `docs/` - markdown sources; `docs/render.buzz` renders them into the
  static site at `docs/gen/` (generated, NOT committed - .github/workflows/publish-site.yaml
  renders it at deploy time)
- `console/` - the native console PWA (standalone pnpm project); read
  `console/README.md` before touching it (CSS naming, PF conventions)

## Local gotchas

- A fresh worktree needs `mise trust` before `magus run lint` will pass. mise keys
  trust on the config file's ABSOLUTE path, so every worktree gets its own
  untrusted `mise.toml`, and the tools it provides (golangci-lint, govulncheck,
  shellcheck) then exit non-zero. The failure names neither mise nor trust in the
  target's error - it surfaces as `govulncheck exited 1` - so check the run log
  before believing the tool found something. Trust the tree once instead of per
  worktree: `mise settings add trusted_config_paths ~/Repos/magus`.
- Verifying the console locally: the service worker precaches aggressively and
  serves stale bundles. Serve `console/gen` on a fresh port, or unregister the
  SW and clear caches before trusting what you see.
- Leftover `.claude/worktrees/` copies duplicate spell sources and trip
  MGS1002 when running magus at the repo root; remove dead worktrees first.
- `magus affected ci` has one known local-environment failure that is NOT your
  change: the doctor console check needs a running daemon. `magus run lint .` is
  otherwise GREEN as of 2026-08-02 - the "pre-existing lint findings" this file
  used to warn about are gone, so treat a lint failure as yours rather than
  assuming it is background noise.
- Need one value out of a magus command? ASK MAGUS FOR IT: `-o name` (ids, one
  per line), `-o json` (the record), `-o template='{{.Field}}'` (one field).
  Never reach for a filter - the guard denies piping AND redirecting magus
  output (`| head`, `> file`, `>> file`, `2>&1`), because a pipe also replaces
  the exit status with the last stage's, so a FAILING gate reads as exit 0. Use
  `-s/--silent` to bound it (silent already trims, so filtering it is never the
  careful version). You never need to capture console output: every run persists
  its full log and a failure prints that path, and `--tee <file>` mirrors only
  STRUCTURED output (`-o json|yaml|jsonl|template`), never console text.
  `magus query output <ref>` is the one command you may pipe or redirect: a raw
  captured tool log has no schema to project.
- Run magus from the workspace and NAME the project (`magus run <target>
  <project>`) instead of `cd`-ing to it. Running magus inside a temp or
  scratchpad COPY of the tree is denied outright: the verdict would describe a
  tree nobody ships, generated files land in the copy, the cache splits, and
  duplicated spell sources trip MGS1002. A different workspace is `--root
  <path>`; a pristine tree is a throwaway `git worktree`, not a copy.

## Rules

- No emojis anywhere: code, output, commits, docs.
- User-facing message strings are plain ASCII (no em-dashes, curly quotes);
  code comments are exempt. Docs frontmatter is plain ASCII too.
- Never hand-edit generated files (`gen/` dirs, `MAGUS.md`, `docs/gen/`); change
  the source of truth and regenerate. Generated output lives in a `gen/` dir and
  carries no extra suffix - the directory is the signal.
- Docs site follows classless Pico: semantic HTML, minimal custom classes,
  no inline styles.
- Language-level changes in `libs/gopherbuzz/` must match upstream Buzz behavior.
- Buzz code is tested with in-file `test "..." {}` blocks; run via
  `magus buzz -t <file>`.
- Commits: subject line only, no area prefix, no Co-Authored-By trailer;
  join multiple ideas with semicolons. Never push unless explicitly asked.
- Git is the orchestrator's job: do VCS ops yourself, never delegate git to a
  subagent, and run mutating subagents isolated or serialized. Never a whole-tree
  git op (`stash`/`reset`/`checkout .`/`clean`) to verify a build - it wipes a
  concurrent agent's untracked work. See the magus-vcs-hygiene skill.
- Code that exists ONLY to keep older data, artifacts, or callers working carries a
  `compat(until: <condition>):` comment, in the shape of the existing
  `optimization:` prefix. Three things, or it is not auditable: what it supports,
  the condition that retires it, and how you would OBSERVE that dropping it is
  safe. A date is not a condition - "no store still serves ed25519 envelopes" is.
  Secondary sites say `compat: see <the primary site>` rather than restating it.
  Use Go's `// Deprecated:` instead when the thing is an exported API callers
  should stop using - staticcheck's SA1019 already enforces that one, and it is
  the wrong marker for an internal branch nobody should stop reaching. Do not mark
  code that merely LOOKS like a shim: resolving an old-width output ref is not
  compat, because attempt ids share that shape and there is nothing to rip out.
- `TODO`, `FIXME`, and `BUG` comments are WELCOME and stay. Do not add `godox` (or
  any linter that reports them) to `.golangci.yml`: a gate that fails because a
  note exists is red by design, and banning the note does not do the work. This is
  a standing decision, not an oversight - the `compat(until:)` marker above is the
  one comment convention worth enforcing, and even it is served better by a
  structural check than by keyword matching.
- Regenerate in the SAME commit as the source change that invalidated the output.
  A one-word `Name:` edit in a `std/` descriptor left four generated files stale
  and three tests red across three commits; `go generate` reaches
  `cmd/magus-utils`, so it needs no magus binary. CI runs generate as a drift
  gate, so a split commit is also a red CI you did not have to have.
- Before "fixing" behavior that looks wrong, look for the test that pins it.
  `TestCheckExecRequiresReadNotExec` exists to say exec-collapsing-into-read is
  deliberate and names the "fix" as a known mistake. Roughly one review finding in
  ten is wrong this way. See the `magus-local-development` skill for the rest of this method.

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
