# Arms

An arm is a provisioning recipe applied to a fresh worktree of the benchmark
fixture (a magus-ified TypeScript monorepo, not this repository), plus a probe
that proves the worktree is in that arm. The two arms differ ONLY in the agent
surface; the fixture, the model, the prompts, and the budget caps are held
identical by the runner.

```sh
benchmarks/agent/arms/<arm>/provision.sh <worktree> <magus-binary>
benchmarks/agent/arms/<arm>/probe.sh <worktree>
```

`<magus-binary>` is passed rather than resolved so one recipe can pin two
different builds: that is how a change to magus itself is A/B'd on the same arm.
Provisioning writes `<worktree>/.benchmark/env.sh`, which the runner sources
after scrubbing the environment to its whitelist.

Both provisioning scripts are idempotent: they clear `.benchmark/`, `.claude/`,
`MAGUS.md`, `CLAUDE.md`, and `.mcp.json` before writing.

## Switches, as implemented

| Component      | rampant (ARM-0)                                     | full (ARM-1)                                                                                                                                       |
| -------------- | --------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| Skills         | none installed; no `.claude/skills` at all          | `magus agent install .claude/skills --force --prune` with the given binary (30 files: a short body per skill plus its `-full` twin)                |
| Guard hooks    | 3 PreToolUse entries kept, each `"command": "true"` | the shipped `magus-guard-{command,path,observe}.sh` copied to `.claude/hooks/`, wired by matcher, pinned to the given binary via `GUARD_MAGUS_BIN` |
| MAGUS.md       | absent                                              | `magus describe graph -o markdown`, rendered with the given binary                                                                                 |
| CLAUDE.md      | `repo-description.md` verbatim                      | the same file plus the marker-bounded block `magus agent starter` prints                                                                            |
| CLI hints      | `MAGUS_HINTS_ENABLED=false`                         | on (unset)                                                                                                                                         |
| MCP            | not registered                                      | not registered (see below)                                                                                                                         |
| Activity trail | rotation off                                        | rotation off                                                                                                                                       |
| magus binary   | first on PATH                                       | first on PATH                                                                                                                                      |

The hook entries survive in the rampant arm on purpose. Removing them would
change the shape of `settings.json` as well as its behavior, and pointing them
at a missing binary is worse still: the templates then inject a "magus guard is
NOT running" notice, which is itself context this arm must not have. `true` is
silent and exits 0.

`repo-description.md` is shared by both arms so that the routing block, and not
the repo prose, is what the arms differ by.

## What is NOT switched: MCP

Neither arm registers an MCP server, and neither runs a persistent daemon. The
full arm is skills + hooks + routing index; MCP is a v2 switch.

The reason is that MCP registration is user-scoped rather than in-repo, so it is
not a property of the worktree a provisioning script owns, and its cost lands as
tool SCHEMAS in the system prompt, which transcripts never show. Measuring it
honestly needs the invisible-floor estimate the metrics extractor does not have
yet. Holding it at zero for both arms keeps the paired deltas clean.

How the absence is guaranteed, and how the probes prove it:

- Provisioning writes `<worktree>/.benchmark/mcp.json` naming no servers and
  exports its path as `RUNNER_MCP_CONFIG` in `env.sh`. The runner hands that
  file to the host with `--strict-mcp-config`, which makes it the only MCP
  configuration the session sees: the operator's user-scoped registrations are
  never consulted.
- The tree carries no `.mcp.json`, which is the project scope.
- Both probes require the exported file to exist and to name an empty server
  set, and the tree to carry no `.mcp.json`.
- Relocating `CLAUDE_CONFIG_DIR` to an empty directory is NOT how this is done,
  and it was, once: the host keeps its login beside its MCP registrations, so
  the first pilot's six sessions all ended in "Not logged in". The file plus
  the strict flag isolates MCP without touching credentials.

## Confounds

These are properties of the surface, not defects in the scripts, and they are
why the ablation is a 0/1 ladder rather than a menu of independent switches:

- **Guard advisories name skills.** An advisory says "load the magus-query
  skill". Hooks-on with skills-off is a surface that routes to files that are
  not there, so it is a partial surface rather than a clean control.
- **Skill bodies name MCP tools.** The installed skills tell an agent to prefer
  `magus_query` and friends and to check `magus status --probe=mcp`. With MCP
  unregistered, the full arm's agent is told about tools it does not have; the
  skills say to continue with the CLI fallback, which is what it will do.
- **The CLAUDE.md block and the skills share one content digest.** They are
  stamped together by `magus agent install`, so they move as one version.
- **The magus binary is on PATH in BOTH arms.** The rampant agent may discover
  and use `magus` on its own. That is signal, not contamination.

## What the probes check, and what they cannot

Both probes exit non-zero at the first failing check, naming it.

The full probe leans on `magus doctor` for currency (`agent-skills` must be ok,
`guard-wiring` must be ok and must name the worktree's own `settings.json`,
which is what proves the copied templates carry the current
`magus-guard-template` version). Doctor runs with `HOME` pointed at
`<worktree>/.benchmark/home`, because it also reads home-scoped hook configs:
without that, a stale hook config belonging to whoever runs the benchmark would
decide an arm's verdict.

Two limits worth knowing:

- Doctor decides "installed" from ONE anchor skill and grades the skills it
  finds, so a skill that goes missing after provisioning is invisible to it.
  Provisioning records the installed count and the probe re-counts.
- `magus-guard-observe.sh` is not among the basenames doctor grades. The probe
  checks that it exists and carries a version marker; its currency rides on the
  other two, since all three are copied from one checkout in one step.
