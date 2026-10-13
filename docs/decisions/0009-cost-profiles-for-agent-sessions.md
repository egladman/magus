---
title: "ADR 0009: cost profiles for agent sessions"
order: 9
description: The guard keeps a long-running root session from spending its context on work a cheaper worker could do. One level, set by magus\guard.profile and defaulting to light, picks how much of that advice a workspace sees; the account's billing and model facts live in magus.yaml, detected from the host and never from an environment variable; a person may raise the level and never lower it.
tags: [adr, decision, agents, guard, cost, config, profiles, stall]
status: accepted (in progress)
date: 2026-10-13
---

# ADR 0009: cost profiles for agent sessions

Each item below carries its own state: _done_ is in the tree, _planned_ is decided and
queued, and _not built_ was weighed and declined.

## Context

A root agent session re-sends its whole context on every call, mostly as cache reads, so
its cost grows with the square of its length. Measured on this repository's Claude Code
transcripts from 2026-10-01 to 2026-10-13, over 26 root sessions and 569 subagents:

- The root ran on Opus for 99 percent of its calls and carried 35 percent of all input
  tokens, 5.53 billion.
- Nine of 26 roots reached about 960 thousand tokens of context. The median peak was 615
  thousand, and the first call of a session started at 79 thousand.
- Recursive search, whole-file reads, `git diff`, `git log` and non-gate `magus run` calls
  made up 63 percent of the root's tool-result characters, and 105 streaks of eight or more
  of those calls ran without a spawn.
- 164 Opus spawns carried a prompt shaped like a scout, a read-only job or research.

The same person wants two policies, chosen by account and not by repository. On a personal
subscription the bill is theirs and the root may carry more. On a shared account the cost is
visible to a team. Billing is a fact about the account. How hard the guard pushes is a
policy. They are two settings in two homes, and magus has to work outside this repository,
for a person who only wanted a task runner.

## Decision

### One level, set by a call

_Done._ `magus\guard.profile(level)` picks the level the guard's compiled rules start from:
`off`, `light`, `standard` or `strict`. A level sets the rules it names, and a rule named in
`magus\guard.builtins` overrides the level's setting for that rule. It is declared once at
the top level of the root magusfile. Unset is `light`.

When the magusfile is tracked, the stricter of the committed and the working-tree level
applies, as it does for each `builtins` setting, so a loosening takes effect only once it is
committed.

A person's `agents.profile` in `magus.yaml` can raise the level and never lower it, and of
the tiers that set it the highest applies. The workspace sets a floor and the person who
pays chooses to go above it.

### Facts live in `magus.yaml`

_Done._ The `agents` section holds what only the person or the account owner knows:

```yaml
agents:
  billing: metered # metered or quota; unset is unknown
  profile: strict # a person's level, raise only
  models: # adds or overrides rows of the model table
    claude-haiku-5-5: { window: 1000000, cache_read: 0.01, price_step: 100000, step_factor: 5 }
```

- Billing is detected from the host before it is read from config: a hook environment with
  `ANTHROPIC_API_KEY`, `CLAUDE_CODE_USE_BEDROCK` or `CLAUDE_CODE_USE_VERTEX` is metered. An
  OAuth sign-in is ambiguous, because a usage-billed enterprise seat and a Max plan look
  alike, so it stays unknown unless configured.
- A declared `quota` in an environment that shows metered billing is an error naming both,
  because the declared value is not what is happening.
- A workspace's own `magus.yaml` wins over the user tier for billing, as layering always
  does, so a company repository can state `metered` for everyone who works in it.
- The fields carry no command-line flag, and there is no environment variable.
- Billing changes thresholds and wording and never which rules run.

### Model facts come from three places, in order

_Done._ The host payload comes first, then `agents.models`, then a table magus ships in
`internal/agent/model`, keyed by model id prefix. A window the host states is never
second-guessed. A model that matches no row gets no dollar wording and no window-relative
threshold, and the absolute token thresholds still apply. `magus doctor` reports a table
older than 120 days. The table is hand-written and never fetched at run time.

### The root is the main thread

_Done._ The cost rules judge the main thread of a session, not an unleased session, which
also covers a subagent without a lease and a person. A guard request carries the thread
(`main`, `sub` or `unknown`), whether the host said so, and the context the host reported.

| host        | main thread when                           | context tokens from                            |
| ----------- | ------------------------------------------ | ---------------------------------------------- |
| Claude Code | the call has no `agent_id`                 | the last usage line of the main transcript     |
| OpenCode    | the session has no parent                  | the plugin sends the last message's tokens     |
| Codex       | no lease, and the session recorded a spawn | the transcript's last `token_count` event      |
| Cursor      | no lease, and the session recorded a spawn | `preCompact`; the last value stands until then |

An unknown thread caps every verdict at advice. A wrong deny blocks real work, and a wrong
advice costs one line.

### The rules and their levels

_Planned._ Six rules join the compiled defaults, each disableable by name and each firing
once per session per threshold:

| rule                        | light                            | standard                                                            | strict                                                                        |
| --------------------------- | -------------------------------- | ------------------------------------------------------------------- | ----------------------------------------------------------------------------- |
| root-context-budget         | advise once at half the window   | advise at 250k metered, 400k quota, 300k unknown                    | advise at 150k, 250k or 200k; deny delegable commands from 300k, 450k or 350k |
| root-heavy-command          | off                              | advise after 8 delegable calls since a spawn                        | advise at 5, deny from 8                                                      |
| root-result-cap             | advise once above 40k characters | 20k, once per tool class                                            | 10k, once per tool class                                                      |
| root-repeat                 | off                              | advise on the second identical read or command                      | deny the third                                                                |
| spawn-names-model           | off                              | advise on a spawn naming no model                                   | deny a spawn naming none, or one differing from its job's model               |
| spawn-principal-for-economy | off                              | advise when a read-only job's spawn names or inherits Opus or Fable | the same, plus Explore-type spawns; never deny                                |

The delegable commands are recursive search, `cat` and `sed -n` of source, a whole-file host
read, `git diff` and `git log -p` without `--stat` or `--name-only`, non-gate `magus run`
and web fetches. The root may always spawn, run the `magus job` verbs, read a ref with
`magus query output`, run the final gate, load a skill, read orientation files and edit.

`spawn-principal-for-economy` keys on declared facts, the job's `read_only` and the host's
agent type, and never denies, because a read-only design job can need Opus. A workspace that
wants prompt-shape matching writes it as its own `magus\guard.spawn` rule, which already
receives the prompt.

### The stall rule

_Planned._ A worker that is blocked on something only the root can clear needs one clear
exit, not a longer wait. The `stall` rule classifies a refusal the rules already make as
fixable by the worker or not. When an unfixable one fires for a leased worker, the verdict
ends with "exit the job with this as an unresolved risk", and one attention row is filed per
lease and rule, so the root and the person see the block once.

- `light` ships this level only. The levels above it, a repeated refusal and a run of failed
  calls, stay with `standard` and `strict`, and `strict` uses lower counts.
- A row states facts: the model the job ran on and how often it was blocked. It never
  suggests a model. The root decides whether to re-fork the job with another.
- Per-model ladders wait for two weeks of shadow verdicts. A model tier gets one only where
  it stalls at least twice as often per thousand calls as the others.
- The rule never denies, and it adds no verb or flag.

### Record what happened, so profiles are tuned from data

_Done_ for the trail fields and the `session show` cost sections; _planned_ for shadow
verdicts and metrics. Each agent call records its thread, context tokens, model, billing
and result size. Each root call records what every level would have decided, whichever is
active, so "how often would `strict` have denied" has an answer with no experiment. Reports
fold into `magus session show` and its JSON, and metrics go through the existing OTEL path.

| item                                                                           | State     |
| ------------------------------------------------------------------------------ | --------- |
| `magus\guard.profile`, the levels, and the stricter-wins merge                 | done      |
| `agents.billing`, `agents.profile`, `agents.models` and host billing detection | done      |
| the model table, `agents.models` merge and the doctor age check                | done      |
| thread, context and billing on guard requests and trail records                | done      |
| cost sections in `magus session show`                                          | done      |
| the six cost rules and the level table                                         | planned   |
| the `stall` rule at level 3, and the levels above it                           | planned   |
| shadow verdicts per level, and OTEL metrics                                    | planned   |
| per-model stall ladders                                                        | not built |

### Doctrine checked

[Doctrine](../doctrine.md) holds each of these up against this decision.

- [Human-first is the AI integration](../doctrine.md#human-first-is-the-ai-integration):
  relied on. The level is a magusfile call and the facts are `magus.yaml` keys, with no
  command-line flag that feeds a model.
- [Friction that carries meaning](../doctrine.md#friction-that-carries-meaning): relied on.
  Each advisory fires once per session per threshold, because an advisory that fires on
  every call is a toll.
- [Misconfiguration is an error, never a warning](../doctrine.md#misconfiguration-is-an-error-never-a-warning):
  relied on. An unknown level is MGS1045, a model row with no window fails at load, and a
  declared `quota` under a metered environment is an error.
- [Agents propose, humans dispose](../doctrine.md#agents-propose-humans-dispose): relied on.
  A stall files a row for the root and the person to read, and never picks the remedy.
- [Told, never guessed](../doctrine.md#told-never-guessed): departed from, in two places.
  Billing is read from the host's environment, and a Codex or Cursor session that records a
  spawn and has no lease is taken for a root. Both change advice only: wording, thresholds
  and an unknown thread's verdict capped at advice. Neither changes what a command does, and a
  declared value that contradicts a detected one is an error. The departure was Eli's
  decision on 2026-10-13, and `agents.billing` stays as the way to state the mode outright.
- [The outer harness stays outside](../doctrine.md): pending, because the principle sits on
  an unmerged branch (`feat/proofread-library-improvements-87c508`). The decision is meant to
  agree with it. The guard judges and informs, and no rule here trims, rewrites or proxies
  what the host's loop sends the model.

## Alternatives

- **The level as a reserved key in `magus\guard.builtins`.** It needed no new call and reused
  the stricter-wins merge, and the first design recommended it. The decision went the other
  way: a key that is not a rule name inside a map of rule names would need validation and
  `describe rules` to treat one key differently, and a call of its own keeps rule names and
  the level apart. Reopen if the level and the rules need to be declared together.
- **An environment variable for billing.** One export per account would set it for every
  workspace in a shell. A stray export would flip every workspace silently, and the
  environment namespace is open. Host detection plus `magus.yaml` covers both accounts a
  person has. Reopen if a host appears that bills per token and shows nothing in its hook
  environment.
- **A default of `off`.** `light` says one thing once per session, at half the window and
  at one 40 thousand character result, and only reaches a person already running an agent
  with magus hooks. Reopen if a measured share of `light` advisories draws a complaint.
- **Matching spawn prompts for economy.** A pattern over 164 prompts is a guess, and a
  read-only design job looks the same as a search job. Reopen with a field that says which
  a job is.
- **Fetching prices at run time.** The Models API needs a key a subscription user lacks,
  and a hook already misses its 10 second budget under load. Reopen if the table goes stale
  faster than the 120 day age check and `agents.models` can cover.
- **Stall behavior per model before data.** The 14 days to 2026-10-13 showed 3.0 to 5.1 runs
  of three or more failed calls per thousand across Haiku, Sonnet, Opus and Fable, and about
  30 percent of every tier's refusals repeated one already given. The Haiku sample is 33
  agents. Reopen when shadow verdicts show a tier stalling at twice the rate of the rest.

The stall rule's declined designs, a model that watches workers, the root as the watcher,
and a denial from the gauge among them, are rows in
[A record of refusals](../doctrine.md#a-record-of-refusals).

## Consequences

- A workspace with no agent hooks never meets any of this. A person with one agent and no
  subagents sees at most one advisory per session at `light`.
- `magus.yaml` gains a section only an account owner can fill, and a work laptop's user
  tier can say `metered` for every repository on it.
- The thread rules depend on what each host reports. Codex and Cursor cannot name the main
  thread, so their cost rules advise at most.
- Reading the main transcript on a post-tool hook changes an old invariant, that magus never
  reads that file. The read is bounded to a tail and runs after the call, so a pre-call rule
  reads recorded state only.
- The shipped model table needs an update when a model's price changes, or a person's
  `agents.models` row, and the doctor check says when it has aged.

## Open questions

- Which prompt-cache time-to-live each host uses. It sets what an idle root costs while it
  waits on workers, and the numbers here assume the five minute write price.
- Whether Claude Code hooks inherit `ANTHROPIC_API_KEY` and the Bedrock and Vertex
  variables in every launch mode, which billing detection relies on.
