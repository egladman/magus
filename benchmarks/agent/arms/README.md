# Arms

An arm is a provisioning recipe applied to a fresh worktree of the benchmark
fixture (a magus-ified TypeScript monorepo, not this repository), plus a probe
that proves the worktree is in that arm. The two arms differ ONLY in the agent
integration; the fixture, the model, the prompts, and the budget caps are held
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

| Component      | rampant (ARM-0)                                     | full (ARM-1)                                                                                                                               |
| -------------- | --------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| Skills         | none installed; no `.claude/skills` at all          | `magus agent install .claude/skills --force --skill-form both` with the given binary (a short body per skill plus its `-full` twin)        |
| Guard hooks    | 3 PreToolUse entries kept, each `"command": "true"` | the shipped `magus-{command,path,observe}.buzz` copied to `.claude/hooks/`, wired by matcher, pinned to the given binary via `__MAGUS_BIN` |
| MAGUS.md       | absent                                              | `magus describe graph -o markdown`, rendered with the given binary                                                                         |
| CLAUDE.md      | `repo-description.md` verbatim                      | the same file plus the marker-bounded block `magus agent starter` prints                                                                   |
| CLI hints      | `MAGUS_HINTS_ENABLED=false`                         | on (unset)                                                                                                                                 |
| MCP            | not registered                                      | not registered (see below)                                                                                                                 |
| Activity trail | rotation off                                        | rotation off                                                                                                                               |
| magus binary   | first on PATH                                       | first on PATH                                                                                                                              |

The hook entries survive in the rampant arm on purpose. Removing them would
change the shape of `settings.json` as well as its behavior, and pointing them
at a missing binary is worse still: the templates then inject a "magus guard is
NOT running" notice, which is itself context this arm must not have. `true` is
silent and exits 0.

`repo-description.md` is shared by both arms so that the routing block, and not
the repo prose, is what the arms differ by.

## Skill-form arms

Three arms reuse the full recipe and differ from it, and from each other, only
in the skill bodies. Each is a pair of few-line wrappers over `full/`:

| Arm            | Skills                                                                    |
| -------------- | ------------------------------------------------------------------------- |
| `skills-full`  | `--skill-form full`: one full body per skill                              |
| `skills-short` | `--skill-form short`: one short body per skill                            |
| `skills-c1`    | the directory `BENCH_C1_DIR` names, copied verbatim into `.claude/skills` |

`full/provision.sh` reads the choice from `BENCH_SKILL_FORM` (default `both`)
and `BENCH_SKILLS_DIR`, which wins when set, and records it in
`.benchmark/skills.source`. `skills-c1` fails to provision when `BENCH_C1_DIR`
is unset. `compress.manifest` runs the three over the eight fixture tasks.

## What is not switched: MCP

Neither arm registers an MCP server, and neither runs a persistent daemon. The
full arm is skills + hooks + routing index; MCP is a v2 switch.

MCP registration is user-scoped rather than in-repo, so it is not a property of the worktree a provisioning script owns, and its cost lands as
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

These are properties of the integration, not defects in the scripts, and they are
why the ablation is a 0/1 ladder rather than a menu of independent switches:

- **Guard advisories name skills.** An advisory says "load the magus-query
  skill". Hooks-on with skills-off is an integration that routes to files that are
  not there, so it is a partial integration rather than a clean control.
- **Skill bodies name MCP tools.** The installed skills tell an agent to prefer
  `client` (`magus\query`) and to check `magus status --probe=mcp`. With MCP
  unregistered, the full arm's agent is told about tools it does not have; the
  skills say to continue with the CLI fallback, which is what it does.
- **The CLAUDE.md block and the skills share one content digest.** They are
  stamped together by `magus agent install`, so they move as one version.
- **The magus binary is on PATH in BOTH arms.** The rampant agent may discover
  and use `magus` on its own. That is signal, not contamination.

## What the probes check, and what they cannot

Both probes exit non-zero at the first failing check, naming it.

`magus doctor` grades installed skills and guard wiring only through a harness
wired in a root magusfile, and the fixture carries none, so the full probe
proves currency itself:

- The skills must be byte-identical to what the given binary renders for the
  recorded form (rendered again into a temporary directory and compared), or to
  the custom directory for `skills-c1`.
- The command wired for `Bash` in `settings.json` must answer a synthetic
  `PreToolUse` event, run with `HOME` pointed at `<worktree>/.benchmark/home` so
  that a hook config belonging to whoever runs the benchmark cannot decide the
  verdict.

Two limits worth knowing:

- Nothing grades the `magus-guard-template` version against the binary any
  more; the probe checks the marker is present, and provisioning copies the
  templates from the same checkout the binary is meant to be built from.
- The templates import `lib/hook.buzz` relative to themselves, so provisioning
  copies it beside them and the probe checks it is there: without it every hook
  fails to load.
