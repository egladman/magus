# Agent-benchmark task corpus

Eight tasks over the enriched `large-monorepo` fixture, one directory each:

| file          | contract                                                                                          |
| ------------- | ------------------------------------------------------------------------------------------------- |
| `task.md`     | the prompt handed to the agent, in plain engineering language                                     |
| `seed.sh`     | `seed.sh <worktree>` puts a fresh fixture worktree into the start state; idempotent               |
| `check.sh`    | `check.sh <worktree>` exits 0 on success; deterministic, end-state only, never reads a transcript |
| `solution.sh` | `solution.sh <worktree>` applies the oracle solution                                              |
| `meta.json`   | `category`, `budget_usd`, `max_turns`, and an `answer_key` where one exists                       |

`task-lib.sh` beside them carries the three helpers the scripts share (seed a
worktree, apply a literal edit, grade an `ANSWER.md` bullet list as a set).

Seed state lives on `task/<id>` branches of `benchmarks/large-monorepo/gen/repo`,
cut by `benchmarks/large-monorepo/tasks.sh`, which `setup.sh` calls. Nothing here is
hand-applied to `gen/`: `rm -rf gen/ && ./setup.sh` reproduces the whole corpus.

A run is a detached `git worktree add` of that clone, so several runs of one task can
proceed at once and no run has `node_modules`. Every check is stdlib node plus git for
that reason, and runs identically whatever agent surface the arm provisions.

## Answer format

The interrogation and catch-up tasks are graded on an `ANSWER.md` the agent writes at
the worktree root, never on an LLM judge. `task.md` fixes the format, and the bullet
list under `## Answer` is compared as a SET: a path that does not belong costs exactly
what a missing one does, so listing everything scores no better than listing nothing.

## Task validity and outcome validity

Per the Agentic Benchmark Checklist (arXiv 2507.02825). Every row was run through
`seed -> check (must fail) -> solution -> check (must pass)` in a fresh worktree.

| id                          | category      | budget           | what check.sh asserts                                                                                                                                                             | why a null agent fails                                                                                      | why the oracle passes                                                         |
| --------------------------- | ------------- | ---------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------- |
| `platform-http-consumers`   | interrogation | $1.50 / 40 turns | the `## Answer` bullet set equals the three feature libraries that import `packages/platform/http`                                                                                | no `ANSWER.md` is written, and a shotgun list of every feature library fails on the extras                  | it writes exactly the three the bridge manifest fixes                         |
| `config-change-rebuild-set` | interrogation | $1.50 / 40 turns | the `## Answer` bullet set equals the four apps downstream of `packages/platform/config`                                                                                          | no `ANSWER.md`; listing all five apps fails on `apps/warp-drive-manager`, which is genuinely not downstream | it names the four apps reachable through the config and http edges            |
| `merge-config-falsy`        | bugfix        | $3.00 / 60 turns | a held-out test that `false`, `0`, `''` and `null` overrides win while `undefined` is skipped, plus the four platform packages' own tests                                         | the seeded `if (!value) continue;` drops every falsy override and the held-out assertions fail              | restoring the `=== undefined` guard makes both suites green                   |
| `url-query-encoding`        | bugfix        | $3.00 / 60 turns | a held-out test that keys and values are percent-encoded, keys stay sorted, an empty query returns the base, and `request` routes through it                                      | the seeded raw concatenation emits `q=a b&c=d`, so the encoding assertions fail                             | restoring `encodeURIComponent` on both halves makes them pass                 |
| `rename-logger-factory`     | generated     | $4.00 / 80 turns | `createLogger` appears nowhere in the tree, a held-out test that `makeLogger` and every platform caller and bridge still work, the packages' own tests, and `gen-api.mjs --check` | the name is still in thirteen files (seven sources, six generated summaries), so the grep gate trips first  | renaming across the tree and regenerating the summaries clears all four gates |
| `add-metrics-percentile`    | generated     | $3.00 / 60 turns | a held-out nearest-rank test (including the empty case and no mutation of the caller's array), the packages' own tests, and `gen-api.mjs --check`                                 | `percentile` is not exported, so the held-out module fails to load                                          | adding the function and regenerating leaves the summaries consistent          |
| `changes-since-baseline`    | catchup       | $2.00 / 50 turns | the `## Answer` bullet set equals the four directories whose own files changed since the `release-2` tag, and `## Behavior change` names `timeoutMs` and `2500`                   | no `ANSWER.md`; naming the downstream dependents instead of the changed directories fails on the extras     | the four commits after the tag touch exactly those four directories           |
| `regenerate-api-summaries`  | discovery     | $2.00 / 40 turns | 14 `gen/api.md` files exist, `gen-api.mjs --check` is clean, and the platform tests pass                                                                                          | two summaries are deleted and three hand-edited, so the population check trips and the drift check follows  | one run of the generator rewrites all 14 byte-for-byte                        |

The population check on the discovery task is there because the drift check alone
would pass vacuously against a tree with the `.mjs` sources deleted. The bugfix and
generated tasks grade against a held-out copy of the test rather than the one in the
tree, so gutting the repo's tests is not a way through.

## Changing a task invalidates prior numbers

The answer keys are fixed by `benchmarks/large-monorepo/enrich/bridges.tsv` and by the
seed commits in `tasks.sh`. Editing either changes what a correct answer is, so treat
the corpus as frozen once scored runs begin.
