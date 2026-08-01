# Handoff: the agent harness (guard hooks, skills, templates)

Written 2026-07-28. Branch `feat/plans-buzz-parity-handoff-9b8119`, 24 commits on
top of `add-install` (`48564ef3`). Tree clean, `go test ./...` green, nothing
pushed. Companion to `buzz-parity-handoff.md`, which covers the gopherbuzz work
from the first half of the same session (27 -> 48 of 83).

## Start here

```sh
go build -o /tmp/magus ./cmd/magus     # HEAD; the hook resolves this via GUARD_MAGUS_BIN
go test ./... -count=1
/tmp/magus agent hook -o name -- 'git stash'      # deny
/tmp/magus agent hook --path -- libs/diag/MAGUS.md # deny (declared output)
```

## The one-line thesis

Skills describe; hooks enforce. Everything below moved a rule from prose into a
mechanism that fails a build or blocks a call.

## What the guard does now

`magus agent hook` (cmd/magus/agent.go), one binary, no host-specific code.

**deny** - 5 whole-tree git ops; editing a path that is a DECLARED OUTPUT
(`--path`, the only non-heuristic rule: it reads the target's declared outputs).

**advise** - staging/committing; path-scoped `git checkout --`/`git restore`;
raw language tools (Go/Python/JS/Rust/proto); piping magus's own output;
repo-wide `grep -r`/`rg`/`find -name`; `cd X && magus ...`; `git push`.

Notes that cost real time to learn:

- The guard was DEAD in this repo before this session: `.claude/settings.json`
  gated on `command -v magus`, and CLAUDE.md says not to expect a release binary
  on PATH. It exited 0 before evaluating anything. If it ever looks quiet, check
  that first - it now emits a visible "guard is NOT running" notice instead.
- Code search is ADVISE, not deny. Denying was tried and reverted: magus has no
  raw-text search (`query` is domain-fuzzy and returns 0 for a code symbol,
  `refs` needs an exact symbol, `x` is an interactive picker), so denying grep
  removed a capability with no replacement and blocked three real lookups.
- `git push` advises, `git commit` does not gate on ci. Committing mid-mess is
  ordinary; a gate there gets tuned out.

## Templates: one implementation, per-host wrappers

`docs/guides/integrations/agents/` - beside the guide that documents them, and a
magus project of its own so they are linted (tsc + Biome mirroring
libs/textsearch's rules + shellcheck in sh mode, pinned in mise.toml).

`.claude/settings.json` INVOKES these files rather than copying them, so
dogfooding exercises what a reader downloads. Two repo-root tests
(`hookdocs_test.go`) fail if the config stops referencing a template or the guide
stops embedding one verbatim.

Overrides: `HOST_EVENT_PATH`, `HOST_RESPONSE`, `GUARD_UNAVAILABLE_RESPONSE`,
`GUARD_MAGUS_BIN`. That last name avoids `MAGUS_*` on purpose - that space is
magus's own config surface.

| host | command | declared-output | verified how |
| --- | --- | --- | --- |
| Claude Code | yes | yes | dogfooded all session |
| Codex | yes | per OpenAI docs | ran Codex-shaped events through the unmodified scripts |
| OpenCode | yes | yes | tool names + `filePath` read out of an installed 1.18.5 binary; `opencode debug config` confirms plugin load |
| Cursor | yes | DETECT only | Cursor has no pre-write hook; `afterFileEdit` reports instead |

Codex needed no new script - its event and reply match Claude Code's, so only
`codex-hooks.json` is Codex-specific. Cursor is ONE self-contained file covering
both its events, because three downloads is how a guard ends up not installed.

## Tool fixes that came out of this

- `--silent` was ignored at startup (applyDisplay reads global.quiet, which only
  saw `--quiet`). Also switched from a plain tail to the diagnostic excerpt.
  Passing run 14 -> 0 lines; failing 162 (`-q`) -> 52, diagnostics first.
- Target args were SPREAD as positional params, so `(ctx, args: [str])` bound the
  first string and dropped the rest - contradicting every magusfile in the repo.
- Args did not key the cache: a run with different args replayed the previous
  result. That is a correctness bug, now fixed (hashed like charms, in order;
  empty args hash unchanged so no entry was invalidated).
- Fixing the cache revealed spell-op passthrough already worked; it had been
  masked by replayed cache hits.
- `--dry-run` omitted forwarded args (WithExtraArgs applied after the dry-run
  branch returned).
- `magus mcp` printed Codex and Claude Desktop setup. Now prints only the neutral
  facts. `TestNoHostSpecificBehaviorInCode` enforces it: a host name is allowed
  only where it names something on disk.

## Skills

`magus-buzz` added (SkillVersion 17 -> 18, installed to all four destinations,
`magus graph verify` clean). Written from executed output; writing it found that
`strings` is case-conversion not Go's strings, JSON is `stringify`/`parse`, and
host modules import by BARE name. Its first rule is to ask
`magus describe module <name>` rather than guess.

## Open, in priority order

1. **`magus\project` has no `name` key.** The root project's label falls back to
   the checkout directory basename, so regenerating MAGUS.md in a worktree
   renames the root project to the worktree's name. This is why generated
   indexes are not reproducible from a worktree, and why agents keep reverting
   them. Fix: add `name` to `knownProjectOptionKeys` (internal/interp/bindings/
   project_ns.go), a `Name` field on `types.Project`, and populate the graph node
   from it. Verification needs a non-worktree checkout.
2. **Root `MAGUS.md` classifies as `role=source`** while every nested MAGUS.md is
   `role=output`, though both are declared with `ctx.outputs`. The path guard
   surfaced it; role resolution is the suspect.
3. **`docs/gen/**` and `docs/MAGUS.md` want regenerating on main.** Left dirty
   and reverted here: gen is temporal footer churn, MAGUS.md is mixed (correctly
   records the new project, also carries the worktree-name poisoning from 1).
4. **Cursor `preToolUse`** is documented as blocking for any tool and may allow
   real prevention rather than detection. Its payload shape is not established;
   do not guess it.
5. **Memory hook** - recommendation was capture-not-replication: an advise on
   writes to a host memory path suggesting `magus memory put` for magus-domain
   decisions. ~10 lines, not implemented, awaiting a decision.
6. **Hook-template transclusion.** The guide embeds templates verbatim, enforced
   by a test. Real transclusion in the SSG would remove the duplication; it
   touches three render paths (page.buzz:437, nav.buzz:205, blog.buzz:62).

## Caveat worth carrying forward

Every guard rule in this handoff was written and dogfooded by one session that
was itself governed by those rules. They blocked several of my own commands and
the routing repeatedly produced better answers than the habit would have - but
none of it has been reviewed by anyone else, and the mistakes caught during the
session (host names in a proposed CLI surface, `/tmp/magus` in a public
template, `MAGUS_BIN` squatting the config namespace, a tautological test) were
all caught by the user, not by me.

## DECIDED, not yet implemented: teach instead of prevent

Agreed at the end of the session, ready to apply.

The principle to encode, which generalizes the git rules rather than adding a
new idea:

  magus denies only what cannot be undone. Everything else it explains.

The 5 whole-tree git ops stay DENY: they destroy uncommitted and untracked work,
including a concurrent agent's, and nothing recovers it. The declared-output rule
becomes ADVISE: editing a generated file is wasteful, not destructive - you
regenerate and it is gone - so it fails the cannot-be-undone test. Reword it in
teaching voice ("that file is generated; the next run overwrites it; change the
source instead"), which is what the Cursor variant already says because
afterFileEdit gave it no choice.

Evidence this is the right direction, from this session: the deny on repo-wide
search blocked three legitimate lookups and had to be reverted, while the
advisories demonstrably changed behavior (they routed the session to `magus
refs`, which answered better than grep would have). Blocking cost turns;
teaching changed what got done. The agent could also have classified the file
itself with `magus describe file`, which is exactly the case where teaching
beats preventing.

Side effect worth having: Cursor stops being the degraded host. Its detect-only
afterFileEdit becomes the reference behavior rather than a consolation prize,
and the parity matrix collapses - every host does the same thing, at slightly
different moments.

Change set: flip the `--path` verdict in agentHookCmd from deny to advise,
reword denyGeneratedWrite (rename it), update TestEvaluateBashGuard and
TestAgentHookPathMode, simplify the parity table in
docs/guides/integrations/agents.md, and adjust magus-guard-path.sh's comment
about only ever denying. Roughly 20 minutes.

## DONE (superseding the section above)

Both landed in `27444cd9`.

Teach-instead-of-prevent is implemented: the declared-output rule advises,
`adviseGeneratedWrite` replaced `denyGeneratedWrite`, the path template emits
`additionalContext`, the Cursor script checks for `advise`, and the guide now
opens with the principle. Only the 5 whole-tree VCS ops still deny.

`magus.project` accepts `name`, and the root magusfile declares `"name":
"magus"`. `describe.go` prefers the declared name over the directory-derived
label. MAGUS.md regenerates from a worktree as `## Project: magus`; the
worktree-poisoning that made generated output un-regenerable is gone.

Still open: root MAGUS.md classifying as role=source (item 2); docs/gen
regeneration on main (item 3, now unblocked by the name fix); Cursor preToolUse
(item 4); the memory hook (item 5); SSG transclusion (item 6).
